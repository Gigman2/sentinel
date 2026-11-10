package demodata_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/support/demodata"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

const adminEmail = "admin@uppsel.test"

// worker settings for fakeWorker.
type worker struct {
	// failOnce, if set, makes the first job of the incident with this
	// fingerprint fail.
	failOnce string
}

// fakeWorker stands in for the Python worker: it completes each due pending
// job with a new revision holding the findings the demo data acts on. Like
// the real one it polls, not instantly, so events sent without waiting for
// an analysis coalesce into one job.
func fakeWorker(ctx context.Context, t *testing.T, pool *pgxpool.Pool, w worker) {
	failed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
		if w.failOnce != "" && !failed {
			tag, err := pool.Exec(ctx, `
				UPDATE analysis_jobs SET status = 'failed', attempts = max_attempts, last_error = 'boom', finished_at = now(), updated_at = now()
				WHERE status = 'pending' AND incident_id = (SELECT id FROM incidents WHERE fingerprint = $1)`, w.failOnce)
			if err != nil && ctx.Err() == nil {
				t.Errorf("fake worker: %v", err)
				return
			}
			failed = tag.RowsAffected() > 0
		}
		_, err := pool.Exec(ctx, `
			WITH job AS (
			    UPDATE analysis_jobs SET status = 'succeeded', attempts = attempts + 1, finished_at = now(), updated_at = now()
			    WHERE id = (SELECT id FROM analysis_jobs WHERE status = 'pending' AND next_attempt_at <= now()
			                ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
			    RETURNING id, incident_id),
			rev AS (
			    INSERT INTO analysis_revisions (incident_id, job_id, revision_number)
			    SELECT job.incident_id, job.id,
			           (SELECT COALESCE(max(revision_number), 0) + 1 FROM analysis_revisions WHERE incident_id = job.incident_id)
			    FROM job RETURNING id)
			INSERT INTO findings (revision_id, ordinal, kind, rule_id, text)
			SELECT rev.id, f.ordinal, f.kind, f.rule_id, f.text FROM rev, (VALUES
			    (0, 'hypothesis', 'deployment_near_onset', 'A deployment may be related.'),
			    (1, 'unknown', 'no_error_evidence', 'No error events were found.'),
			    (2, 'next_check', 'compare_by_version', 'Compare by version.')) AS f (ordinal, kind, rule_id, text)`)
		if err != nil && ctx.Err() == nil {
			t.Errorf("fake worker: %v", err)
			return
		}
	}
}

func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testutil.NewDB(t)
	if _, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: adminEmail}); err != nil {
		t.Fatal(err)
	}
	return pool
}

func config(pool *pgxpool.Pool) demodata.Config {
	return demodata.Config{Pool: pool, OrgSlug: "uppsel", AdminEmail: adminEmail, Wait: 10 * time.Second, Poll: 10 * time.Millisecond}
}

func run(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := demodata.Run(context.Background(), config(pool)); err != nil {
		t.Fatal(err)
	}
}

// startWorker runs fakeWorker until the test ends.
func startWorker(t *testing.T, pool *pgxpool.Pool, w worker) {
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go fakeWorker(ctx, t, pool, w)
}

func query(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// snapshot counts everything the demo data writes.
func snapshot(t *testing.T, pool *pgxpool.Pool) string {
	return query(t, pool, `SELECT concat_ws(' ',
		(SELECT count(*) FROM components), (SELECT count(*) FROM integration_component_mappings),
		(SELECT count(*) FROM component_dependencies), (SELECT count(*) FROM operational_events),
		(SELECT count(*) FROM incidents), (SELECT count(*) FROM incidents WHERE status = 'resolved'),
		(SELECT count(*) FROM analysis_jobs), (SELECT count(*) FROM analysis_revisions),
		(SELECT count(*) FROM finding_feedback), (SELECT count(*) FROM next_check_status))`)
}

func TestDemoDataCoversTheEngineerView(t *testing.T) {
	pool := setup(t)
	startWorker(t, pool, worker{})

	run(t, pool)

	incident := func(fingerprint string) string {
		return query(t, pool, `SELECT id::text FROM incidents WHERE fingerprint = $1`, fingerprint)
	}
	checkout := incident("checkout-error-rate")

	for name, c := range map[string]struct{ sql, want string }{
		"incidents by status and severity": {`SELECT string_agg(status || ':' || severity, ',' ORDER BY first_seen_at) FROM incidents`,
			"resolved:critical,resolved:warning,resolved:error,open:error,open:critical,open:info"},
		"checkout escalated, two signals": {`SELECT severity || ' ' || (SELECT count(*) FROM incident_events e WHERE e.incident_id = i.id AND e.reason IN ('trigger', 'repeat_trigger'))
			FROM incidents i WHERE id = $1`, "critical 2"},
		"checkout revisions, in order": {`SELECT string_agg(j.reason, ',' ORDER BY r.revision_number) FROM analysis_revisions r
			JOIN analysis_jobs j ON j.id = r.job_id WHERE r.incident_id = $1`, "incident_created,repeat_trigger,manual_retry"},
		"checkout feedback on the latest revision": {`SELECT r.revision_number || ' ' || f.rule_id || ' ' || ff.verdict
			FROM finding_feedback ff JOIN findings f ON f.id = ff.finding_id JOIN analysis_revisions r ON r.id = f.revision_id
			WHERE r.incident_id = $1`, "3 deployment_near_onset useful"},
		"checkout check done on the latest revision": {`SELECT r.revision_number || ' ' || f.rule_id
			FROM next_check_status n JOIN findings f ON f.id = n.finding_id JOIN analysis_revisions r ON r.id = f.revision_id
			WHERE r.incident_id = $1`, "3 compare_by_version"},
	} {
		if got := query(t, pool, c.sql, args(c.sql, checkout)...); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}

	if got := query(t, pool, `SELECT string_agg(j.reason, ',' ORDER BY r.revision_number) FROM analysis_revisions r
		JOIN analysis_jobs j ON j.id = r.job_id WHERE r.incident_id = $1`, incident("payment-auth-failures")); got != "incident_created,late_event" {
		t.Errorf("late deployment revisions: %q", got)
	}
	if got := query(t, pool, `SELECT resolution_source || ' ' || left(resolution_note, 13) FROM incidents WHERE id = $1`,
		incident("notify-delay")); got != "manual SMTP provider" {
		t.Errorf("manual resolution: %q", got)
	}
	for _, fp := range []string{"ledger-latency", "payment-provider-timeouts"} {
		if got := query(t, pool, `SELECT resolution_source FROM incidents WHERE id = $1`, incident(fp)); got != "alert" {
			t.Errorf("%s resolved by %q", fp, got)
		}
	}
	if got := query(t, pool, `SELECT ff.verdict FROM finding_feedback ff JOIN findings f ON f.id = ff.finding_id
		JOIN analysis_revisions r ON r.id = f.revision_id WHERE r.incident_id = $1`, incident("search-index-lag")); got != "incorrect" {
		t.Errorf("search feedback: %q", got)
	}
	if got := query(t, pool, `SELECT disposition FROM operational_events WHERE fingerprint = 'search-stale-results'`); got != "unmatched_resolution" {
		t.Errorf("unmatched resolution: %q", got)
	}
	if got := query(t, pool, `SELECT count(*) FROM component_dependencies`); got != "5" {
		t.Errorf("dependencies: %s", got)
	}
	if got := query(t, pool, `SELECT extract(day FROM now() - min(first_seen_at))::int FROM incidents`); got != "6" {
		t.Errorf("oldest incident %s days ago", got)
	}

	// Running it again changes nothing.
	before := snapshot(t, pool)
	run(t, pool)
	if after := snapshot(t, pool); after != before {
		t.Errorf("rerun changed data:\nbefore %s\nafter  %s", before, after)
	}
}

// args passes the incident ID only to queries that take a parameter.
func args(sql, id string) []any {
	if strings.Contains(sql, "$1") {
		return []any{id}
	}
	return nil
}

func TestDemoDataWithoutAWorkerStops(t *testing.T) {
	pool := setup(t)
	c := config(pool)
	c.Wait = 200 * time.Millisecond
	if err := demodata.Run(context.Background(), c); !errors.Is(err, demodata.ErrWorkerNotRunning) {
		t.Fatalf("got %v, want ErrWorkerNotRunning", err)
	}
}

// A run that stopped partway is finished by a later one, with every event
// where the first run placed the story, however much later it resumes.
func TestDemoDataResumesAtTheFirstRunsTimes(t *testing.T) {
	pool := setup(t)
	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	c := config(pool)
	c.Now, c.Wait = func() time.Time { return first }, 200*time.Millisecond
	if err := demodata.Run(context.Background(), c); !errors.Is(err, demodata.ErrWorkerNotRunning) {
		t.Fatalf("first run: got %v, want it to stop for want of a worker", err)
	}

	startWorker(t, pool, worker{})
	run(t, pool) // now, an hour later

	at := func(key string) time.Time {
		var ts time.Time
		if err := pool.QueryRow(context.Background(), `SELECT occurred_at FROM operational_events WHERE source_event_id = $1`,
			"demo-data-"+key).Scan(&ts); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return ts.UTC()
	}
	for key, want := range map[string]time.Time{
		"old-deploy":      first.Add(-(6*24*time.Hour + 9*time.Minute)),
		"old-alert":       first.Add(-6 * 24 * time.Hour),
		"payments-alert":  first.Add(-3 * time.Hour),
		"payments-deploy": first.Add(-(3*time.Hour + 8*time.Minute)),
	} {
		if got := at(key); !got.Equal(want) {
			t.Errorf("%s at %s, want %s", key, got, want)
		}
	}
	if got := query(t, pool, `SELECT count(*) FROM incidents`); got != "6" {
		t.Errorf("incidents after resuming: %s", got)
	}
}

// A failed analysis is requeued once, and the story carries on.
func TestDemoDataRetriesAFailedAnalysis(t *testing.T) {
	pool := setup(t)
	startWorker(t, pool, worker{failOnce: "search-index-lag"})
	run(t, pool)
	if got := query(t, pool, `SELECT string_agg(j.status || ':' || j.reason, ',' ORDER BY j.created_at) FROM analysis_jobs j
		JOIN incidents i ON i.id = j.incident_id WHERE i.fingerprint = 'search-index-lag'`); got != "failed:incident_created,succeeded:manual_retry" {
		t.Errorf("search jobs: %q", got)
	}
}

func TestDemoDataSkipsADisabledIntegration(t *testing.T) {
	pool := setup(t)
	if _, err := pool.Exec(context.Background(), `UPDATE integrations SET enabled = false WHERE name = 'monitor'`); err != nil {
		t.Fatal(err)
	}
	var logged string
	c := config(pool)
	c.Log = func(format string, args ...any) { logged = format }
	if err := demodata.Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if got := query(t, pool, `SELECT count(*) FROM components`); got != "3" || !strings.Contains(logged, "disabled") {
		t.Errorf("components %s, log %q", got, logged)
	}
}
