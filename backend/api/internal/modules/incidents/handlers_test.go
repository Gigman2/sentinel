package incidents_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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
