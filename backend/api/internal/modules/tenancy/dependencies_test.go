package tenancy_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

type depFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
	s    seed.Result
}

func newDepFixture(t *testing.T) depFixture {
	t.Helper()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	return depFixture{t: t, pool: pool, h: server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}), s: s}
}

// component creates a component in the seeded project and returns its ID.
func (f depFixture) component(slug, env, kind string) string {
	f.t.Helper()
	body := map[string]any{"slug": slug, "display_name": slug, "environment": env}
	if kind != "" {
		body["kind"] = kind
	}
	w := testutil.PostJSON(f.t, f.h, "/v1/projects/"+f.s.ProjectID+"/components", f.s.AdminToken, body)
	if w.Code != http.StatusCreated {
		f.t.Fatalf("component %s: %d %s", slug, w.Code, w.Body)
	}
	return testutil.Decode[struct {
		ID string `json:"id"`
	}](f.t, w).ID
}

func (f depFixture) add(from, to string) *httptest.ResponseRecorder {
	f.t.Helper()
	return testutil.PostJSON(f.t, f.h, "/v1/projects/"+f.s.ProjectID+"/dependencies", f.s.AdminToken, map[string]string{"from": from, "to": to})
}

func (f depFixture) mustAdd(from, to string) {
	f.t.Helper()
	if w := f.add(from, to); w.Code != http.StatusCreated {
		f.t.Fatalf("add %s → %s: %d %s", from, to, w.Code, w.Body)
	}
}

type downstream struct {
	Component struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Kind string `json:"kind"`
	} `json:"component"`
	Depth int     `json:"depth"`
	Via   *string `json:"via_component_id"`
}

// openIncident opens an incident on payment-service and returns its ID.
func (f depFixture) openIncident() string {
	f.t.Helper()
	w := testutil.PostJSON(f.t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken, map[string]any{
		"schema_version": "1", "source_event_id": "a-1", "type": "alert.triggered",
		"occurred_at": time.Now().UTC().Format(time.RFC3339), "external_component_id": "payments-prod",
		"environment": "production", "severity": "critical", "fingerprint": "f", "summary": "Payment errors",
	})
	if w.Code != http.StatusCreated {
		f.t.Fatalf("ingest: %d %s", w.Code, w.Body)
	}
	return *testutil.Decode[struct {
		IncidentID *string `json:"incident_id"`
	}](f.t, w).IncidentID
}

func (f depFixture) detailDeps(incidentID string) []downstream {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+incidentID, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[struct {
		Dependencies []downstream `json:"dependencies"`
	}](f.t, w).Dependencies
}

func TestDependencyValidation(t *testing.T) {
	f := newDepFixture(t)
	api, svc := f.s.Components["checkout-api"], f.s.Components["payment-service"]
	staging := f.component("payment-service", "staging", "")

	// The seed already registers checkout-api → payment-service.
	if w := f.add(api, svc); w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "duplicate" {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"self":              {api, api},
		"cross environment": {api, staging},
		"other project":     {api, other.Components["payment-service"]},
		"unknown component": {api, "2dd80324-2e0e-421e-95f0-f848067c4124"},
		"not an id":         {"nope", svc},
	} {
		if w := f.add(c[0], c[1]); w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_dependency" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}

	// A new edge is created, listed and removed.
	ext := f.component("fraud-check", "production", "external")
	w := f.add(api, ext)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := testutil.Decode[struct {
		From, To    struct{ ID, Kind string }
		Environment string
	}](t, w)
	if created.From.ID != api || created.To.ID != ext || created.To.Kind != "external" || created.Environment != "production" {
		t.Fatalf("created: %+v", created)
	}
	list := testutil.Decode[struct {
		Items []struct{ From, To struct{ Slug string } }
	}](t, testutil.GetJSON(t, f.h, "/v1/projects/"+f.s.ProjectID+"/dependencies", f.s.AdminToken)).Items
	if len(list) != 3 {
		t.Fatalf("list: %+v", list)
	}
	del := "/v1/projects/" + f.s.ProjectID + "/dependencies/" + api + "/" + ext
	if w := testutil.SendJSON(t, f.h, http.MethodDelete, del, f.s.AdminToken, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := testutil.SendJSON(t, f.h, http.MethodDelete, del, f.s.AdminToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d %s", w.Code, w.Body)
	}

	// A bad kind on component create.
	w = testutil.PostJSON(t, f.h, "/v1/projects/"+f.s.ProjectID+"/components", f.s.AdminToken,
		map[string]any{"slug": "x-svc", "display_name": "X", "environment": "production", "kind": "vendor"})
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_field" {
		t.Fatalf("bad kind: %d %s", w.Code, w.Body)
	}
}

// Two hops from the incident's component, each component once at its
// shortest depth; a cycle back to the start or within the graph ends there.
func TestDependencyTwoHopsCycleSafe(t *testing.T) {
	f := newDepFixture(t)
	svc, provider := f.s.Components["payment-service"], f.s.Components["payment-provider"]
	ledger := f.component("ledger", "production", "")
	bank := f.component("bank-api", "production", "external")
	far := f.component("far-away", "production", "")
	f.mustAdd(svc, ledger)                              // second direct dependency
	f.mustAdd(ledger, bank)                             // depth 2 via ledger
	f.mustAdd(bank, far)                                // depth 3: not shown
	f.mustAdd(ledger, svc)                              // cycle back to the start
	f.mustAdd(provider, ledger)                         // ledger is also reachable at depth 2; shown once, at depth 1
	f.mustAdd(f.s.Components["checkout-api"], provider) // upstream edges don't matter

	deps := f.detailDeps(f.openIncident())
	got := map[string]downstream{}
	for _, d := range deps {
		if _, seen := got[d.Component.Slug]; seen {
			t.Fatalf("%s listed twice: %+v", d.Component.Slug, deps)
		}
		got[d.Component.Slug] = d
	}
	if len(got) != 3 {
		t.Fatalf("deps: %+v", deps)
	}
	if d := got["payment-provider"]; d.Depth != 1 || d.Via != nil || d.Component.Kind != "external" {
		t.Errorf("payment-provider: %+v", d)
	}
	if d := got["ledger"]; d.Depth != 1 || d.Via != nil {
		t.Errorf("ledger: %+v", d)
	}
	if d := got["bank-api"]; d.Depth != 2 || d.Via == nil || *d.Via != ledger {
		t.Errorf("bank-api: %+v", d)
	}
	if deps[0].Depth != 1 || deps[len(deps)-1].Depth != 2 {
		t.Errorf("not ordered by depth: %+v", deps)
	}
}

func TestDependenciesOtherOrg404(t *testing.T) {
	f := newDepFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/projects/" + f.s.ProjectID + "/dependencies"
	api, svc := f.s.Components["checkout-api"], f.s.Components["payment-service"]
	for name, w := range map[string]*httptest.ResponseRecorder{
		"list":   testutil.GetJSON(t, f.h, base, other.AdminToken),
		"add":    testutil.PostJSON(t, f.h, base, other.AdminToken, map[string]string{"from": svc, "to": api}),
		"delete": testutil.SendJSON(t, f.h, http.MethodDelete, base+"/"+api+"/"+svc, other.AdminToken, nil),
		// Through their own project, naming our components.
		"delete via own project": testutil.SendJSON(t, f.h, http.MethodDelete,
			"/v1/projects/"+other.ProjectID+"/dependencies/"+api+"/"+svc, other.AdminToken, nil),
	} {
		if w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM component_dependencies WHERE project_id = $1`, f.s.ProjectID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("edges after other-org calls: %d %v", n, err)
	}
}

// External components show their display name, so lists sort by it.
func TestDependenciesOrderedByDisplayName(t *testing.T) {
	f := newDepFixture(t)
	svc := f.s.Components["payment-service"]
	mk := func(slug, name string) string {
		w := testutil.PostJSON(t, f.h, "/v1/projects/"+f.s.ProjectID+"/components", f.s.AdminToken,
			map[string]any{"slug": slug, "display_name": name, "environment": "production", "kind": "external"})
		if w.Code != http.StatusCreated {
			t.Fatalf("component %s: %d %s", slug, w.Code, w.Body)
		}
		return testutil.Decode[struct{ ID string }](t, w).ID
	}
	f.mustAdd(svc, mk("a-vendor", "Zeta Pay"))
	f.mustAdd(svc, mk("z-vendor", "Acme Bank"))

	var slugs []string
	for _, d := range f.detailDeps(f.openIncident()) {
		slugs = append(slugs, d.Component.Slug)
	}
	if i, j := slices.Index(slugs, "z-vendor"), slices.Index(slugs, "a-vendor"); i < 0 || j < 0 || i > j {
		t.Fatalf("detail order: %v", slugs)
	}
	list := testutil.Decode[struct {
		Items []struct{ To struct{ Slug string } }
	}](t, testutil.GetJSON(t, f.h, "/v1/projects/"+f.s.ProjectID+"/dependencies", f.s.AdminToken)).Items
	slugs = nil
	for _, d := range list {
		slugs = append(slugs, d.To.Slug)
	}
	if i, j := slices.Index(slugs, "z-vendor"), slices.Index(slugs, "a-vendor"); i < 0 || j < 0 || i > j {
		t.Fatalf("list order: %v", slugs)
	}
}

// A component deleted while a dependency on it is being added gives a
// 400, not a server error. The delete holds its row lock until the insert
// is waiting on it, then commits.
func TestAddDependencyComponentDeletedConcurrently(t *testing.T) {
	f := newDepFixture(t)
	ctx := context.Background()
	svc := f.s.Components["payment-service"]
	doomed := f.component("doomed", "production", "")

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM components WHERE id = $1`, doomed); err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- f.add(svc, doomed) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND query LIKE '%INSERT INTO component_dependencies%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("insert never waited on the delete's lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if w := <-done; w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_dependency" {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
}
