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

// fakeWorker stands in for the Python worker: it completes each due pending
// job with a new revision holding the findings the demo data acts on. Like
// the real one it polls, not instantly, so events sent without waiting for
// an analysis coalesce into one job.
func fakeWorker(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
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

func setup(t *testing.T) (*pgxpool.Pool, seed.Result) {
	t.Helper()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: adminEmail})
	if err != nil {
		t.Fatal(err)
	}
	return pool, s
}

func run(t *testing.T, pool *pgxpool.Pool, s seed.Result) {
	t.Helper()
	if err := demodata.Run(context.Background(), demodata.Config{
		Pool: pool, Seed: s, AdminEmail: adminEmail, Wait: 10 * time.Second, Poll: 10 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
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
	pool, s := setup(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go fakeWorker(ctx, t, pool)

	run(t, pool, s)

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
	run(t, pool, s)
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
	pool, s := setup(t)
	err := demodata.Run(context.Background(), demodata.Config{
		Pool: pool, Seed: s, AdminEmail: adminEmail, Wait: 200 * time.Millisecond, Poll: 10 * time.Millisecond,
	})
	if !errors.Is(err, demodata.ErrWorkerNotRunning) {
		t.Fatalf("got %v, want ErrWorkerNotRunning", err)
	}
}
