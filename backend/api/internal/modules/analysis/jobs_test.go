package analysis_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/modules/analysis"
	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

var now = time.Date(2026, 11, 8, 14, 9, 0, 0, time.UTC)

type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
	s    seed.Result
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }})
	return fixture{t: t, pool: pool, h: h, s: s}
}

// ingest posts an event and expects 201; it returns the decoded result.
func (f fixture) ingest(integrationID, token string, body map[string]any) (eventID string, incidentID *string) {
	f.t.Helper()
	w := testutil.PostJSON(f.t, f.h, "/v1/ingest/"+integrationID+"/events", token, body)
	if w.Code != http.StatusCreated {
		f.t.Fatalf("ingest %v: %d %s", body["source_event_id"], w.Code, w.Body)
	}
	r := testutil.Decode[struct {
		EventID    string  `json:"event_id"`
		IncidentID *string `json:"incident_id"`
	}](f.t, w)
	return r.EventID, r.IncidentID
}

// openIncident ingests one alert and returns the incident and trigger event IDs.
func (f fixture) openIncident() (incidentID, eventID string) {
	f.t.Helper()
	eventID, id := f.ingest(f.s.MonitorIntegrationID, f.s.MonitorToken, map[string]any{
		"schema_version": "1", "source_event_id": "alert-1", "type": "alert.triggered",
		"occurred_at": now.Format(time.RFC3339), "external_component_id": "payments-prod",
		"environment": "production", "severity": "critical", "fingerprint": "payments-errors",
		"summary": "Payment errors affecting checkout",
	})
	return *id, eventID
}

func (f fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f fixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// enqueue runs analysis.Enqueue in its own committed transaction.
func (f fixture) enqueue(incidentID, reason string) (string, error) {
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	id, err := analysis.Enqueue(ctx, tx, incidentID, reason)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func TestEnqueueCoalescesPending(t *testing.T) {
	f := newFixture(t)
	incidentID, _ := f.openIncident() // enqueues incident_created
	var pending string
	if err := f.pool.QueryRow(context.Background(), `SELECT id::text FROM analysis_jobs WHERE incident_id = $1`, incidentID).Scan(&pending); err != nil {
		t.Fatal(err)
	}

	got, err := f.enqueue(incidentID, "manual_retry")
	if err != nil || got != pending {
		t.Fatalf("enqueue with pending job: %s %v, want %s", got, err, pending)
	}
	f.exec(`UPDATE analysis_jobs SET status = 'failed' WHERE id = $1`, pending)
	got, err = f.enqueue(incidentID, "manual_retry")
	if err != nil || got == pending {
		t.Fatalf("enqueue after failure: %s %v, want a new job", got, err)
	}
	if n := f.count(`SELECT count(*) FROM analysis_jobs WHERE id = $1 AND status = 'pending' AND reason = 'manual_retry'`, got); n != 1 {
		t.Fatal("new job not pending manual_retry")
	}
}

// TestEnqueueRetriesWhenLeasedConcurrently forces the race the loop exists
// for: Enqueue's INSERT sees the pending job, then a worker leases it before
// Enqueue can lock it. New work must not be folded into a job already running.
func TestEnqueueRetriesWhenLeasedConcurrently(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	incidentID, _ := f.openIncident()

	// A "worker" holds the pending job's row lock, then leases it.
	worker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Rollback(ctx)
	var leased string
	if err := worker.QueryRow(ctx, `SELECT id::text FROM analysis_jobs WHERE incident_id = $1 FOR UPDATE`, incidentID).Scan(&leased); err != nil {
		t.Fatal(err)
	}

	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := f.enqueue(incidentID, "late_event")
		done <- result{id, err}
	}()
	waitForLockWait(t, f.pool, "%analysis_jobs%FOR NO KEY UPDATE%")

	if _, err := worker.Exec(ctx, `UPDATE analysis_jobs SET status = 'running' WHERE id = $1`, leased); err != nil {
		t.Fatal(err)
	}
	if err := worker.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	r := <-done
	if r.err != nil || r.id == leased {
		t.Fatalf("enqueue: %s %v, want a new job (not the leased %s)", r.id, r.err, leased)
	}
	if n := f.count(`SELECT count(*) FROM analysis_jobs WHERE incident_id = $1 AND status = 'pending'`, incidentID); n != 1 {
		t.Fatalf("pending jobs: %d", n)
	}
}

// waitForLockWait blocks until a backend running a query matching pattern waits on a lock.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool, pattern string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE $1`, pattern).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backend waited on a lock running %q", pattern)
}
