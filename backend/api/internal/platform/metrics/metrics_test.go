package metrics_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	w := testutil.GetJSON(t, h, "/metrics", "")
	if w.Code != http.StatusOK {
		t.Fatalf("/metrics: %d %s", w.Code, w.Body)
	}
	return w.Body.String()
}

func wantLines(t *testing.T, body string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(body, "\n"+l+"\n") {
			t.Errorf("missing %q in:\n%s", l, body)
		}
	}
}

func TestMetricsCountsIngestOutcomes(t *testing.T) {
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	event := func(source, component string) map[string]any {
		return map[string]any{
			"schema_version": "1", "source_event_id": source, "type": "alert.triggered",
			"occurred_at": time.Now().UTC().Format(time.RFC3339), "external_component_id": component,
			"environment": "production", "severity": "critical", "fingerprint": "f", "summary": "Payment errors",
		}
	}
	path := "/v1/ingest/" + s.MonitorIntegrationID + "/events"
	for _, e := range []map[string]any{event("a-1", "payments-prod"), event("a-1", "payments-prod"), event("a-2", "ghost")} {
		testutil.PostJSON(t, h, path, s.MonitorToken, e)
	}

	wantLines(t, scrape(t, h),
		`sentinel_ingest_events_total{outcome="accepted"} 1`,
		`sentinel_ingest_events_total{outcome="replayed"} 1`,
		`sentinel_ingest_events_total{outcome="rejected_unmapped_component"} 1`,
		`sentinel_analysis_jobs{status="pending"} 1`,
		`sentinel_analysis_jobs{status="running"} 0`,
		`sentinel_analysis_jobs{status="succeeded"} 0`,
		`sentinel_analysis_jobs{status="failed"} 0`,
	)
	if body := scrape(t, h); !strings.Contains(body, "\nsentinel_analysis_oldest_pending_seconds ") {
		t.Errorf("no oldest-pending gauge:\n%s", body)
	}

	// Without the database the counters are still served.
	pool.Close()
	body := scrape(t, h)
	wantLines(t, body, `sentinel_ingest_events_total{outcome="accepted"} 1`)
	if strings.Contains(body, "sentinel_analysis_jobs{") {
		t.Errorf("job gauges served without a database:\n%s", body)
	}
}

func TestMetricsOldestPendingIsZeroWhenIdle(t *testing.T) {
	pool := testutil.NewDB(t)
	h := server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	wantLines(t, scrape(t, h), `sentinel_analysis_oldest_pending_seconds 0`, `sentinel_analysis_jobs{status="pending"} 0`)
}
