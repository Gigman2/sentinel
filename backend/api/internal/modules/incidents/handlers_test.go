package incidents_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

var now = time.Date(2026, 11, 8, 14, 9, 0, 0, time.UTC)
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newServer(deps server.Deps) http.Handler {
	deps.Logger = quiet
	deps.Now = func() time.Time { return now }
	return server.New(deps)
}

// createIncident ingests one alert for payment-service and returns the incident ID.
func createIncident(t *testing.T, h http.Handler, s seed.Result) string {
	t.Helper()
	w := testutil.PostJSON(t, h, "/v1/ingest/"+s.MonitorIntegrationID+"/events", s.MonitorToken, map[string]any{
		"schema_version": "1", "source_event_id": "alert-1", "type": "alert.triggered",
		"occurred_at": now.Format(time.RFC3339), "external_component_id": "payments-prod",
		"environment": "production", "severity": "critical", "fingerprint": "payments-errors",
		"summary": "Payment errors affecting checkout",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("ingest: %d %s", w.Code, w.Body)
	}
	return *testutil.Decode[struct {
		IncidentID *string `json:"incident_id"`
	}](t, w).IncidentID
}

func TestIncidentSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	id := createIncident(t, newServer(server.Deps{Pool: pool}), s)

	// A fresh pool and server stand in for a process restart.
	pool2, err := database.Open(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer pool2.Close()
	pool.Close()

	w := testutil.GetJSON(t, newServer(server.Deps{Pool: pool2}), "/v1/incidents/"+id, s.AdminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("after restart: %d %s", w.Code, w.Body)
	}
	if got := testutil.Decode[struct{ ID string }](t, w).ID; got != id {
		t.Fatalf("got incident %s, want %s", got, id)
	}
}

func TestGetIncident_NotFoundCases(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := newServer(server.Deps{Pool: pool})
	id := createIncident(t, h, s)

	cases := map[string]struct{ path, token string }{
		"other org":    {"/v1/incidents/" + id, other.AdminToken},
		"unknown id":   {"/v1/incidents/2dd80324-2e0e-421e-95f0-f848067c4124", s.AdminToken},
		"malformed id": {"/v1/incidents/not-a-uuid", s.AdminToken},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := testutil.GetJSON(t, h, c.path, c.token)
			if w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}

	if w := testutil.GetJSON(t, h, "/v1/incidents/"+id, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
}

func TestManualResolve(t *testing.T) {
	f := newFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	id := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	path := "/v1/incidents/" + id + "/resolve"

	// Another organization cannot see it.
	if w := testutil.PostJSON(t, f.h, path, other.AdminToken, map[string]any{"note": "x"}); w.Code != http.StatusNotFound {
		t.Fatalf("other org: %d %s", w.Code, w.Body)
	}
	if w := testutil.PostJSON(t, f.h, "/v1/incidents/not-a-uuid/resolve", f.s.AdminToken, map[string]any{}); w.Code != http.StatusNotFound {
		t.Fatalf("malformed id: %d", w.Code)
	}
	if w := testutil.PostJSON(t, f.h, path, f.s.AdminToken, map[string]any{"note": strings.Repeat("é", 4001)}); w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_note" {
		t.Fatalf("long note: %d %s", w.Code, w.Body)
	}

	w := testutil.PostJSON(t, f.h, path, f.s.AdminToken, map[string]any{"note": strings.Repeat("é", 4000)})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	got := testutil.Decode[incidentView](t, w)
	if got.Status != "resolved" || got.ResolvedAt == nil || !got.ResolvedAt.Equal(now) ||
		got.Resolution == nil || got.Resolution.Source != "manual" || got.Resolution.Note == nil || len([]rune(*got.Resolution.Note)) != 4000 {
		t.Fatalf("resolved view: %+v", got)
	}

	w = testutil.PostJSON(t, f.h, path, f.s.AdminToken, map[string]any{"note": "again"})
	if w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "already_resolved" {
		t.Fatalf("second resolve: %d %s", w.Code, w.Body)
	}
}

func TestListIncidentsNewestFirstAndPaginated(t *testing.T) {
	f := newFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	a := *f.send(alertEvent("a", "fp-a", "warning", now.Add(-3*time.Minute))).IncidentID
	b := *f.send(alertEvent("b", "fp-b", "critical", now.Add(-2*time.Minute))).IncidentID
	c := *f.send(alertEvent("c", "fp-c", "critical", now.Add(-2*time.Minute))).IncidentID
	d := *f.send(alertEvent("d", "fp-d", "error", now.Add(-time.Minute))).IncidentID

	// b and c share last_seen_at; ties order by id descending.
	tied := []string{b, c}
	if b < c {
		tied = []string{c, b}
	}
	want := []string{d, tied[0], tied[1], a}

	type item struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Severity  string `json:"severity"`
		Title     string `json:"title"`
		Component struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
		} `json:"component"`
		Environment string    `json:"environment"`
		FirstSeenAt time.Time `json:"first_seen_at"`
		LastSeenAt  time.Time `json:"last_seen_at"`
	}
	type page struct {
		Items      []item  `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	base := "/v1/projects/" + f.s.ProjectID + "/incidents"

	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatal("pagination did not end")
		}
		path := base + "?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := testutil.GetJSON(t, f.h, path, f.s.AdminToken)
		if w.Code != http.StatusOK {
			t.Fatalf("list: %d %s", w.Code, w.Body)
		}
		p := testutil.Decode[page](t, w)
		for _, it := range p.Items {
			got = append(got, it.ID)
			if it.ID == d && (it.Status != "open" || it.Severity != "error" || it.Component.Slug != "payment-service" ||
				it.Component.DisplayName == "" || it.Environment != "production" || it.Title == "" || !it.LastSeenAt.Equal(now.Add(-time.Minute))) {
				t.Fatalf("item shape: %+v", it)
			}
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}

	// Status filter.
	if w := testutil.PostJSON(t, f.h, "/v1/incidents/"+a+"/resolve", f.s.AdminToken, map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	for status, n := range map[string]int{"open": 3, "resolved": 1, "": 4} {
		w := testutil.GetJSON(t, f.h, base+"?status="+status, f.s.AdminToken)
		if p := testutil.Decode[page](t, w); w.Code != http.StatusOK || len(p.Items) != n || p.NextCursor != nil {
			t.Fatalf("status=%q: %d, %d items", status, w.Code, len(p.Items))
		}
	}

	errs := []struct {
		name, path, token string
		status            int
		code              string
	}{
		{"other org project", base + "?status=bogus", other.AdminToken, 404, "not_found"},
		{"unknown project", "/v1/projects/2dd80324-2e0e-421e-95f0-f848067c4124/incidents", f.s.AdminToken, 404, "not_found"},
		{"bad status", base + "?status=closed&limit=0", f.s.AdminToken, 400, "invalid_status"},
		{"limit 0", base + "?limit=0&cursor=%21", f.s.AdminToken, 400, "invalid_limit"},
		{"limit 101", base + "?limit=101", f.s.AdminToken, 400, "invalid_limit"},
		{"limit text", base + "?limit=ten", f.s.AdminToken, 400, "invalid_limit"},
		{"cursor not base64", base + "?cursor=%21%21", f.s.AdminToken, 400, "invalid_cursor"},
		{"cursor bad content", base + "?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("yesterday|nope")), f.s.AdminToken, 400, "invalid_cursor"},
	}
	for _, e := range errs {
		w := testutil.GetJSON(t, f.h, e.path, e.token)
		if w.Code != e.status || testutil.ErrorCode(t, w) != e.code {
			t.Fatalf("%s: %d %s", e.name, w.Code, w.Body)
		}
	}
}
