package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

// TestAnalysisTimingsBackfill rolls 0006 back, stores jobs the old way and
// reapplies it: finished jobs get finished_at = updated_at, unfinished ones
// get none, and started_at stays unknown for all of them.
func TestAnalysisTimingsBackfill(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	up, down := gooseSections(t, "0006_analysis_timings.sql")
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}

	var incident string
	err = pool.QueryRow(ctx, `
		WITH ev AS (
		    INSERT INTO operational_events (organization_id, project_id, integration_id, component_id, environment,
		                                    type, source_event_id, occurred_at, received_at, disposition)
		    VALUES ($1, $2, $3, $4, 'production', 'alert.triggered', 'a-1', now(), now(), 'incident_created')
		    RETURNING id)
		INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint, status,
		                       severity, title, first_seen_at, last_seen_at, trigger_event_id, number)
		SELECT $1, $2, $4, 'production', 'fp', 'open', 'error', 'fp', now(), now(), ev.id, 1 FROM ev
		RETURNING id::text`, s.OrgID, s.ProjectID, s.MonitorIntegrationID, s.Components["payment-service"]).Scan(&incident)
	if err != nil {
		t.Fatal(err)
	}
	updated := time.Date(2026, 11, 8, 14, 10, 0, 0, time.UTC)
	jobs := map[string]string{}
	for _, status := range []string{"succeeded", "failed", "running"} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO analysis_jobs (incident_id, reason, status, updated_at) VALUES ($1, 'manual_retry', $2, $3) RETURNING id::text`,
			incident, status, updated).Scan(&id); err != nil {
			t.Fatal(err)
		}
		jobs[status] = id
	}

	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	for status, id := range jobs {
		var started, finished *time.Time
		if err := pool.QueryRow(ctx, `SELECT started_at, finished_at FROM analysis_jobs WHERE id = $1`, id).Scan(&started, &finished); err != nil {
			t.Fatal(err)
		}
		wantFinished := status != "running"
		if started != nil || (finished != nil) != wantFinished || (wantFinished && !finished.Equal(updated)) {
			t.Errorf("%s job: started %v finished %v", status, started, finished)
		}
	}
}
