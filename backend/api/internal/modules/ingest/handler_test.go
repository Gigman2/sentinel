package ingest_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

var now = time.Date(2026, 11, 8, 14, 9, 0, 0, time.UTC)

func setup(t *testing.T) (http.Handler, seed.Result, func(sql string, args ...any) int) {
	t.Helper()
	return setupWith(t, server.Deps{})
}

// setupWith seeds the demo tenant and builds a server from deps (Pool,
// Logger and Now are filled in).
func setupWith(t *testing.T, deps server.Deps) (http.Handler, seed.Result, func(sql string, args ...any) int) {
	t.Helper()
	pool := testutil.NewDB(t)
	res, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	deps.Pool, deps.Logger, deps.Now = pool, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now }
	h := server.New(deps)
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return h, res, count
}

// alert is the PRD §7.2 example payload, mapped to payment-service.
func alert(sourceEventID string) map[string]any {
	return map[string]any{
		"schema_version":        "1",
		"source_event_id":       sourceEventID,
		"type":                  "alert.triggered",
		"occurred_at":           now.Format(time.RFC3339),
		"external_component_id": "payments-prod",
		"environment":           "production",
		"severity":              "critical",
		"fingerprint":           "checkout-error-rate-high",
		"summary":               "Checkout error rate exceeded threshold",
		"source_url":            "https://monitor.example/alerts/7209",
		"attributes":            map[string]any{"error_rate": 0.18},
	}
}

type ingestResponse struct {
	EventID     string  `json:"event_id"`
	Disposition string  `json:"disposition"`
	IncidentID  *string `json:"incident_id"`
	Replayed    bool    `json:"replayed"`
}

type incidentView struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Component struct {
		ID, Slug    string
		DisplayName string `json:"display_name"`
	} `json:"component"`
	Environment    string    `json:"environment"`
	Fingerprint    string    `json:"fingerprint"`
	Status         string    `json:"status"`
	Severity       string    `json:"severity"`
	Title          string    `json:"title"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	EvidenceWindow struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"evidence_window"`
	ResolvedAt     *time.Time      `json:"resolved_at"`
	Resolution     *map[string]any `json:"resolution"`
	TriggerEventID string          `json:"trigger_event_id"`
}

func ingestPath(integrationID string) string { return "/v1/ingest/" + integrationID + "/events" }

func TestIngestAlertCreatesIncident(t *testing.T) {
	h, s, count := setup(t)

	w := testutil.PostJSON(t, h, ingestPath(s.MonitorIntegrationID), s.MonitorToken, alert("alert-7209"))
	if w.Code != http.StatusCreated {
		t.Fatalf("ingest: %d %s", w.Code, w.Body)
	}
	got := testutil.Decode[ingestResponse](t, w)
	if got.EventID == "" || got.IncidentID == nil || got.Disposition != "incident_created" || got.Replayed {
		t.Fatalf("unexpected ingest response %+v", got)
	}

	w = testutil.GetJSON(t, h, "/v1/incidents/"+*got.IncidentID, s.AdminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("get incident: %d %s", w.Code, w.Body)
	}
	inc := testutil.Decode[incidentView](t, w)
	switch {
	case inc.ID != *got.IncidentID, inc.ProjectID != s.ProjectID:
		t.Fatalf("wrong incident or project: %+v", inc)
	case inc.Component.Slug != "payment-service" || inc.Component.ID != s.Components["payment-service"] || inc.Component.DisplayName != "Payment Service":
		t.Fatalf("component %+v", inc.Component)
	case inc.Status != "open" || inc.Severity != "critical" || inc.Environment != "production" || inc.Fingerprint != "checkout-error-rate-high":
		t.Fatalf("fields %+v", inc)
	case inc.Title != "Checkout error rate exceeded threshold":
		t.Fatalf("title %q", inc.Title)
	case !inc.FirstSeenAt.Equal(now) || !inc.LastSeenAt.Equal(now):
		t.Fatalf("first/last seen %s %s, want %s", inc.FirstSeenAt, inc.LastSeenAt, now)
	case !inc.EvidenceWindow.Start.Equal(now.Add(-30*time.Minute)) || !inc.EvidenceWindow.End.Equal(now.Add(15*time.Minute)):
		t.Fatalf("evidence window %+v", inc.EvidenceWindow)
	case inc.TriggerEventID != got.EventID || inc.ResolvedAt != nil || inc.Resolution != nil:
		t.Fatalf("trigger/resolution %+v", inc)
	}
	if inc.FirstSeenAt.Location() != time.UTC {
		t.Fatalf("timestamps must be UTC, got %s", inc.FirstSeenAt.Location())
	}

	if n := count(`SELECT count(*) FROM incident_events WHERE incident_id = $1 AND event_id = $2 AND reason = 'trigger'`, inc.ID, got.EventID); n != 1 {
		t.Fatalf("trigger links: %d", n)
	}
	if n := count(`SELECT count(*) FROM operational_events WHERE id = $1 AND disposition = 'incident_created' AND incident_id = $2 AND received_at = $3`, got.EventID, inc.ID, now); n != 1 {
		t.Fatal("stored event row has the wrong disposition, incident_id or received_at")
	}
}

func TestIngestRejections_NothingStored(t *testing.T) {
	h, s, count := setup(t)

	cases := []struct {
		name, path, token string
	}{
		{"missing token", ingestPath(s.MonitorIntegrationID), ""},
		{"wrong token", ingestPath(s.MonitorIntegrationID), "sk_int_not-the-right-one"},
		{"other integration's token", ingestPath(s.MonitorIntegrationID), s.DeployToken},
		{"malformed integration id", ingestPath("not-a-uuid"), s.MonitorToken},
		{"unknown integration id", ingestPath("2dd80324-2e0e-421e-95f0-f848067c4124"), s.MonitorToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := testutil.PostJSON(t, h, c.path, c.token, alert("alert-"+c.name))
			if w.Code != http.StatusUnauthorized || testutil.ErrorCode(t, w) != "invalid_credential" {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}
	if n := count(`SELECT count(*) FROM operational_events`); n != 0 {
		t.Fatalf("rejected requests stored %d events", n)
	}
}

func TestIngestValidationAndMapping(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)

	cases := []struct {
		name   string
		mutate func(map[string]any)
		status int
		code   string
	}{
		{"wrong schema version", func(b map[string]any) { b["schema_version"] = "2" }, 400, "unsupported_schema_version"},
		{"other event type for now", func(b map[string]any) { b["type"] = "alert.exploded" }, 400, "unsupported_event_type"},
		{"missing fingerprint", func(b map[string]any) { delete(b, "fingerprint") }, 400, "missing_field"},
		{"bad severity", func(b map[string]any) { b["severity"] = "fatal" }, 400, "invalid_field"},
		{"unknown field", func(b map[string]any) { b["colour"] = "red" }, 400, "invalid_json"},
		{"unmapped component", func(b map[string]any) { b["external_component_id"] = "ghost" }, 422, "unmapped_component"},
		{"mapped id, wrong environment", func(b map[string]any) { b["environment"] = "staging" }, 422, "unmapped_component"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := alert("alert-" + c.name)
			c.mutate(body)
			w := testutil.PostJSON(t, h, path, s.MonitorToken, body)
			if w.Code != c.status || testutil.ErrorCode(t, w) != c.code {
				t.Fatalf("got %d %s, want %d %s", w.Code, w.Body, c.status, c.code)
			}
		})
	}
	if n := count(`SELECT count(*) FROM operational_events`); n != 0 {
		t.Fatalf("invalid requests stored %d events", n)
	}
}
