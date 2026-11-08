package integrations_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

var now = time.Date(2026, 11, 8, 14, 9, 0, 0, time.UTC)

type mapping struct {
	ID                  string `json:"id"`
	ExternalComponentID string `json:"external_component_id"`
	Environment         string `json:"environment"`
	ComponentID         string `json:"component_id"`
}

type integration struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Name       string    `json:"name"`
	Capability string    `json:"capability"`
	Enabled    bool      `json:"enabled"`
	Mappings   []mapping `json:"mappings"`
	Credential string    `json:"credential"`
}

type env struct {
	h    http.Handler
	pool *pgxpool.Pool
	a, b seed.Result
	ctx  context.Context
	t    *testing.T
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewDB(t)
	a, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }})
	return env{h: h, pool: pool, a: a, b: b, ctx: ctx, t: t}
}

func (e env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e env) create(t *testing.T, mappings ...map[string]string) integration {
	t.Helper()
	ms := []map[string]string{}
	ms = append(ms, mappings...)
	w := testutil.PostJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", e.a.AdminToken, map[string]any{
		"name": "sentry", "capability": "incident_input", "mappings": ms,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create integration: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[integration](t, w)
}

func (e env) paymentsMapping() map[string]string {
	return map[string]string{"external_component_id": "pay", "environment": "production", "component_id": e.a.Components["payment-service"]}
}

func (e env) alert(t *testing.T, integrationID, token, sourceEventID string) *http.Response {
	t.Helper()
	w := testutil.PostJSON(t, e.h, "/v1/ingest/"+integrationID+"/events", token, map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "alert.triggered",
		"occurred_at": now.Format(time.RFC3339), "external_component_id": "pay", "environment": "production",
		"severity": "critical", "fingerprint": "f", "summary": "s",
	})
	return w.Result()
}

func TestIntegrationCredentialShownOnce(t *testing.T) {
	e := setup(t)
	w := testutil.PostJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", e.a.AdminToken, map[string]any{
		"name": "sentry", "capability": "incident_input", "mappings": []map[string]string{e.paymentsMapping()},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create response must not be cached, got %q", w.Header().Get("Cache-Control"))
	}
	in := testutil.Decode[integration](t, w)
	if !strings.HasPrefix(in.Credential, "sk_int_") || !in.Enabled || len(in.Mappings) != 1 || in.ProjectID != e.a.ProjectID {
		t.Fatalf("created %+v", in)
	}

	var stored string
	if err := e.pool.QueryRow(e.ctx, `SELECT credential_hash FROM integrations WHERE id = $1`, in.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != auth.Hash(in.Credential) {
		t.Fatal("stored hash does not match the returned credential")
	}

	w = testutil.GetJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", e.a.AdminToken)
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, "sk_int_") || strings.Contains(body, stored) || strings.Contains(body, "credential") {
		t.Fatalf("list leaks credential material: %d %s", w.Code, body)
	}
	list := testutil.Decode[struct{ Items []integration }](t, w)
	if len(list.Items) != 3 { // monitor, deploys (seeded) + sentry
		t.Fatalf("list: %+v", list.Items)
	}
}

func TestMappingEnvironmentMustMatchComponent(t *testing.T) {
	e := setup(t)
	staging := map[string]string{"external_component_id": "pay", "environment": "staging", "component_id": e.a.Components["payment-service"]}

	w := testutil.PostJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", e.a.AdminToken, map[string]any{
		"name": "sentry", "capability": "incident_input", "mappings": []map[string]string{staging},
	})
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_mapping" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	msg := testutil.Decode[testutil.ErrorBody](t, w).Error.Message
	if !strings.Contains(msg, "staging") || !strings.Contains(msg, "production") {
		t.Fatalf("message should name both environments: %q", msg)
	}
	if n := e.count(`SELECT count(*) FROM integrations WHERE name = 'sentry'`); n != 0 {
		t.Fatal("failed create left an integration behind")
	}

	in := e.create(t)
	w = testutil.PostJSON(t, e.h, "/v1/integrations/"+in.ID+"/mappings", e.a.AdminToken, staging)
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_mapping" {
		t.Fatalf("add mapping: %d %s", w.Code, w.Body)
	}
	if n := e.count(`SELECT count(*) FROM integration_component_mappings WHERE integration_id = $1`, in.ID); n != 0 {
		t.Fatal("rejected mapping was stored")
	}
}

func TestMappingValidation(t *testing.T) {
	e := setup(t)
	in := e.create(t, e.paymentsMapping())
	path := "/v1/integrations/" + in.ID + "/mappings"

	if w := testutil.PostJSON(t, e.h, path, e.a.AdminToken, e.paymentsMapping()); w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "mapping_exists" {
		t.Fatalf("duplicate mapping: %d %s", w.Code, w.Body)
	}
	otherOrgComponent := map[string]string{"external_component_id": "x", "environment": "production", "component_id": e.b.Components["payment-service"]}
	if w := testutil.PostJSON(t, e.h, path, e.a.AdminToken, otherOrgComponent); w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_mapping" {
		t.Fatalf("other org's component: %d %s", w.Code, w.Body)
	}
	checkout := map[string]string{"external_component_id": "co", "environment": "production", "component_id": e.a.Components["checkout-api"]}
	w := testutil.PostJSON(t, e.h, path, e.a.AdminToken, checkout)
	if w.Code != http.StatusCreated {
		t.Fatalf("valid mapping: %d %s", w.Code, w.Body)
	}
	if m := testutil.Decode[mapping](t, w); m.ID == "" || m.ComponentID != e.a.Components["checkout-api"] {
		t.Fatalf("mapping %+v", m)
	}
	if w := testutil.PostJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", e.a.AdminToken, map[string]any{
		"name": "x", "capability": "telepathy", "mappings": []any{},
	}); w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_field" {
		t.Fatalf("bad capability: %d %s", w.Code, w.Body)
	}
}

func TestPatchReturnsMappings(t *testing.T) {
	e := setup(t)
	in := e.create(t, e.paymentsMapping())

	w := testutil.SendJSON(t, e.h, http.MethodPatch, "/v1/integrations/"+in.ID, e.a.AdminToken, map[string]bool{"enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	got := testutil.Decode[integration](t, w)
	if got.Enabled || len(got.Mappings) != 1 || got.Mappings[0].ExternalComponentID != "pay" || got.Credential != "" {
		t.Fatalf("patched %+v", got)
	}
	if w := testutil.SendJSON(t, e.h, http.MethodPatch, "/v1/integrations/"+in.ID, e.a.AdminToken, map[string]any{}); w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "missing_field" {
		t.Fatalf("empty patch: %d %s", w.Code, w.Body)
	}
}

func TestDisabledIntegrationCannotIngest(t *testing.T) {
	e := setup(t)
	in := e.create(t, e.paymentsMapping())
	if r := e.alert(t, in.ID, in.Credential, "a-1"); r.StatusCode != http.StatusCreated {
		t.Fatalf("enabled integration: %d", r.StatusCode)
	}
	testutil.SendJSON(t, e.h, http.MethodPatch, "/v1/integrations/"+in.ID, e.a.AdminToken, map[string]bool{"enabled": false})

	if r := e.alert(t, in.ID, in.Credential, "a-2"); r.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled integration: %d", r.StatusCode)
	}
	if n := e.count(`SELECT count(*) FROM operational_events WHERE integration_id = $1`, in.ID); n != 1 {
		t.Fatalf("events stored: %d, want only the one sent while enabled", n)
	}
}

func TestRotateCredential(t *testing.T) {
	e := setup(t)
	in := e.create(t, e.paymentsMapping())

	w := testutil.PostJSON(t, e.h, "/v1/integrations/"+in.ID+"/rotate-credential", e.a.AdminToken, "")
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate: %d %s (Cache-Control %q)", w.Code, w.Body, w.Header().Get("Cache-Control"))
	}
	rotated := testutil.Decode[integration](t, w)
	if !strings.HasPrefix(rotated.Credential, "sk_int_") || rotated.Credential == in.Credential {
		t.Fatalf("new credential %q", rotated.Credential)
	}
	if r := e.alert(t, in.ID, in.Credential, "a-old"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old credential: %d", r.StatusCode)
	}
	if r := e.alert(t, in.ID, rotated.Credential, "a-new"); r.StatusCode != http.StatusCreated {
		t.Fatalf("new credential: %d", r.StatusCode)
	}
}

func TestMemberCannotManageIntegrations(t *testing.T) {
	e := setup(t)
	in := e.create(t, e.paymentsMapping())

	memberToken := "sk_usr_member-token-for-tests"
	if _, err := e.pool.Exec(e.ctx, `
		WITH u AS (INSERT INTO users (email, display_name) VALUES ('member@uppsel.test', 'Member') RETURNING id),
		     m AS (INSERT INTO memberships (organization_id, user_id, role) SELECT $1, id, 'member' FROM u)
		INSERT INTO api_tokens (user_id, token_hash) SELECT id, $2 FROM u`, e.a.OrgID, auth.Hash(memberToken)); err != nil {
		t.Fatal(err)
	}

	for name, w := range map[string]interface{ Result() *http.Response }{
		"create": testutil.PostJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", memberToken, map[string]any{"name": "x", "capability": "incident_input", "mappings": []any{}}),
		"patch":  testutil.SendJSON(t, e.h, http.MethodPatch, "/v1/integrations/"+in.ID, memberToken, map[string]bool{"enabled": false}),
		"rotate": testutil.PostJSON(t, e.h, "/v1/integrations/"+in.ID+"/rotate-credential", memberToken, ""),
		"map":    testutil.PostJSON(t, e.h, "/v1/integrations/"+in.ID+"/mappings", memberToken, e.paymentsMapping()),
	} {
		if code := w.Result().StatusCode; code != http.StatusForbidden {
			t.Errorf("member %s: %d, want 403", name, code)
		}
	}
	// Members can still read.
	if w := testutil.GetJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", memberToken); w.Code != http.StatusOK {
		t.Fatalf("member list: %d", w.Code)
	}
}

func TestCrossOrgIntegrationRoutes404(t *testing.T) {
	e := setup(t)
	in := e.create(t, e.paymentsMapping())
	other := e.b.AdminToken

	checks := map[string]int{
		"patch":        testutil.SendJSON(t, e.h, http.MethodPatch, "/v1/integrations/"+in.ID, other, map[string]bool{"enabled": false}).Code,
		"rotate":       testutil.PostJSON(t, e.h, "/v1/integrations/"+in.ID+"/rotate-credential", other, "").Code,
		"map":          testutil.PostJSON(t, e.h, "/v1/integrations/"+in.ID+"/mappings", other, e.paymentsMapping()).Code,
		"list":         testutil.GetJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", other).Code,
		"create":       testutil.PostJSON(t, e.h, "/v1/projects/"+e.a.ProjectID+"/integrations", other, map[string]any{"name": "x", "capability": "incident_input", "mappings": []any{}}).Code,
		"malformed id": testutil.SendJSON(t, e.h, http.MethodPatch, "/v1/integrations/nope", e.a.AdminToken, map[string]bool{"enabled": false}).Code,
	}
	for name, code := range checks {
		if code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	var enabled bool
	if err := e.pool.QueryRow(e.ctx, `SELECT enabled FROM integrations WHERE id = $1`, in.ID).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("other org changed the integration (enabled=%v, err=%v)", enabled, err)
	}
}
