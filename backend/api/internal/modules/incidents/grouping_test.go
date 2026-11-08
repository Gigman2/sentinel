package incidents_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

// fixture is a seeded database and a server in front of it.
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
	return fixture{t: t, pool: pool, h: newServer(server.Deps{Pool: pool}), s: s}
}

type ingestResult struct {
	EventID     string  `json:"event_id"`
	Disposition string  `json:"disposition"`
	IncidentID  *string `json:"incident_id"`
}

type incidentView struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	Resolution  *struct {
		Source string  `json:"source"`
		Note   *string `json:"note"`
	} `json:"resolution"`
}

// alertEvent is an alert.triggered for payment-service with the given fields.
func alertEvent(sourceEventID, fingerprint, severity string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "alert.triggered",
		"occurred_at": at.Format(time.RFC3339Nano), "external_component_id": "payments-prod",
		"environment": "production", "severity": severity, "fingerprint": fingerprint,
		"summary": "Payment errors affecting checkout",
	}
}

func resolvedEvent(sourceEventID, fingerprint string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "alert.resolved",
		"occurred_at": at.Format(time.RFC3339Nano), "external_component_id": "payments-prod",
		"environment": "production", "severity": "info", "fingerprint": fingerprint,
	}
}

// send posts an event from the monitor integration and expects 201.
func (f fixture) send(body map[string]any) ingestResult {
	f.t.Helper()
	w := testutil.PostJSON(f.t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken, body)
	if w.Code != http.StatusCreated {
		f.t.Fatalf("ingest %v: %d %s", body["source_event_id"], w.Code, w.Body)
	}
	return testutil.Decode[ingestResult](f.t, w)
}

func (f fixture) incident(id string) incidentView {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+id, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("get incident: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[incidentView](f.t, w)
}

func (f fixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f fixture) linkReason(incidentID, eventID string) string {
	f.t.Helper()
	var reason string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT reason FROM incident_events WHERE incident_id = $1 AND event_id = $2`, incidentID, eventID).Scan(&reason); err != nil {
		f.t.Fatalf("link %s→%s: %v", eventID, incidentID, err)
	}
	return reason
}

func TestRepeatAlertAttaches(t *testing.T) {
	f := newFixture(t)
	first := f.send(alertEvent("a-1", "payments-errors", "warning", now))
	second := f.send(alertEvent("a-2", "payments-errors", "critical", now.Add(3*time.Minute)))

	if first.Disposition != "incident_created" || second.Disposition != "incident_attached" {
		t.Fatalf("dispositions: %s, %s", first.Disposition, second.Disposition)
	}
	if *second.IncidentID != *first.IncidentID {
		t.Fatalf("repeat opened a second incident")
	}
	inc := f.incident(*first.IncidentID)
	if inc.Severity != "critical" || !inc.FirstSeenAt.Equal(now) || !inc.LastSeenAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("after attach: %+v", inc)
	}
	if got := f.linkReason(inc.ID, second.EventID); got != "repeat_trigger" {
		t.Fatalf("repeat link reason %q", got)
	}
	if n := f.count(`SELECT count(*) FROM operational_events WHERE id = $1 AND incident_id = $2 AND disposition = 'incident_attached'`,
		second.EventID, inc.ID); n != 1 {
		t.Fatal("repeat event outcome not recorded")
	}
}

func TestOutOfOrderRepeatAlert(t *testing.T) {
	f := newFixture(t)
	first := f.send(alertEvent("a-1", "payments-errors", "critical", now))
	// An earlier alert arriving late neither moves the start nor pulls last_seen_at back.
	f.send(alertEvent("a-2", "payments-errors", "critical", now.Add(-10*time.Minute)))

	inc := f.incident(*first.IncidentID)
	if !inc.FirstSeenAt.Equal(now) || !inc.LastSeenAt.Equal(now) {
		t.Fatalf("first_seen %s last_seen %s, want both %s", inc.FirstSeenAt, inc.LastSeenAt, now)
	}
}

func TestLowerSeverityRepeatDoesNotDowngrade(t *testing.T) {
	f := newFixture(t)
	first := f.send(alertEvent("a-1", "payments-errors", "critical", now))
	f.send(alertEvent("a-2", "payments-errors", "info", now.Add(time.Minute)))

	if got := f.incident(*first.IncidentID).Severity; got != "critical" {
		t.Fatalf("severity %s, want critical", got)
	}
}

func TestDifferentComponentOrFingerprintSeparate(t *testing.T) {
	f := newFixture(t)
	base := f.send(alertEvent("a-1", "payments-errors", "critical", now))

	otherComponent := alertEvent("a-2", "payments-errors", "critical", now)
	otherComponent["external_component_id"] = "checkout-prod"

	cases := map[string]map[string]any{
		"other fingerprint": alertEvent("a-3", "payments-latency", "critical", now),
		"other component":   otherComponent,
	}
	for name, body := range cases {
		got := f.send(body)
		if got.Disposition != "incident_created" || *got.IncidentID == *base.IncidentID {
			t.Fatalf("%s: %+v, want a new incident", name, got)
		}
	}
	if n := f.count(`SELECT count(*) FROM incidents WHERE status = 'open'`); n != 3 {
		t.Fatalf("open incidents: %d, want 3", n)
	}
}

func TestConcurrentAlertsOneIncident(t *testing.T) {
	f := newFixture(t)
	const n = 20

	results := make([]ingestResult, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := testutil.PostJSON(t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken,
				alertEvent(fmt.Sprintf("c-%02d", i), "payments-errors", "critical", now.Add(time.Duration(i)*time.Second)))
			codes[i] = w.Code
			if w.Code == http.StatusCreated {
				results[i] = testutil.Decode[ingestResult](t, w)
			}
		}()
	}
	close(start)
	wg.Wait()

	created, attached, opener := 0, 0, 0
	for i, r := range results {
		if codes[i] != http.StatusCreated {
			t.Fatalf("request %d: status %d", i, codes[i])
		}
		switch r.Disposition {
		case "incident_created":
			created, opener = created+1, i
		case "incident_attached":
			attached++
		}
	}
	if created != 1 || attached != n-1 {
		t.Fatalf("created %d, attached %d", created, attached)
	}
	if got := f.count(`SELECT count(*) FROM incidents`); got != 1 {
		t.Fatalf("incidents: %d", got)
	}
	if got := f.count(`SELECT count(*) FROM incident_events`); got != n {
		t.Fatalf("links: %d", got)
	}
	// first_seen_at is the opening alert's time; last_seen_at the latest of all.
	inc := f.incident(*results[0].IncidentID)
	if !inc.FirstSeenAt.Equal(now.Add(time.Duration(opener)*time.Second)) || !inc.LastSeenAt.Equal(now.Add((n-1)*time.Second)) {
		t.Fatalf("window after concurrent attach: %s – %s", inc.FirstSeenAt, inc.LastSeenAt)
	}
}

// TestTriggerRetriesWhenResolvedConcurrently forces the interleaving the
// retry loop exists for: the repeat alert's INSERT sees the open incident,
// then the incident is resolved before the alert can lock it.
func TestTriggerRetriesWhenResolvedConcurrently(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := f.send(alertEvent("a-1", "payments-errors", "critical", now))

	// Hold a row lock on the open incident without changing it yet, so the
	// alert's INSERT still conflicts but its SELECT … FOR NO KEY UPDATE waits.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM incidents WHERE id = $1 FOR UPDATE`, *first.IncidentID); err != nil {
		t.Fatal(err)
	}

	done := make(chan *ingestResult, 1)
	go func() {
		w := testutil.PostJSON(t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken,
			alertEvent("a-2", "payments-errors", "critical", now.Add(time.Minute)))
		if w.Code != http.StatusCreated {
			t.Errorf("repeat alert: %d %s", w.Code, w.Body)
			done <- nil
			return
		}
		r := testutil.Decode[ingestResult](t, w)
		done <- &r
	}()

	waitForLockWait(t, f.pool, "%FOR NO KEY UPDATE%")

	if _, err := tx.Exec(ctx, `UPDATE incidents SET status = 'resolved', resolved_at = $2, resolution_source = 'manual' WHERE id = $1`,
		*first.IncidentID, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	r := <-done
	if r == nil {
		t.FailNow()
	}
	if r.Disposition != "incident_created" || *r.IncidentID == *first.IncidentID {
		t.Fatalf("after concurrent resolve: %+v, want a new incident", r)
	}
	if n := f.count(`SELECT count(*) FROM incidents WHERE status = 'open'`); n != 1 {
		t.Fatalf("open incidents: %d", n)
	}
}

// waitForLockWait blocks until some backend running a query matching
// pattern is waiting on a lock.
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

func TestResolutionAndRecurrence(t *testing.T) {
	f := newFixture(t)
	first := f.send(alertEvent("a-1", "payments-errors", "critical", now.Add(-30*time.Minute)))
	// A resolution that occurred before the last alert still resolves (ADR).
	f.send(alertEvent("a-2", "payments-errors", "critical", now.Add(-20*time.Minute)))
	res := f.send(resolvedEvent("r-1", "payments-errors", now.Add(-21*time.Minute)))

	if res.Disposition != "incident_resolved" || res.IncidentID == nil || *res.IncidentID != *first.IncidentID {
		t.Fatalf("resolution: %+v", res)
	}
	inc := f.incident(*first.IncidentID)
	if inc.Status != "resolved" || inc.ResolvedAt == nil || !inc.ResolvedAt.Equal(now.Add(-21*time.Minute)) ||
		inc.Resolution == nil || inc.Resolution.Source != "alert" {
		t.Fatalf("resolved incident: %+v", inc)
	}
	if got := f.linkReason(inc.ID, res.EventID); got != "resolution" {
		t.Fatalf("resolution link reason %q", got)
	}

	// The same key firing again is a new incident.
	again := f.send(alertEvent("a-3", "payments-errors", "warning", now))
	if again.Disposition != "incident_created" || *again.IncidentID == *first.IncidentID {
		t.Fatalf("recurrence: %+v", again)
	}
}

func TestUnmatchedResolution(t *testing.T) {
	f := newFixture(t)
	open := f.send(alertEvent("a-1", "payments-errors", "critical", now))

	res := f.send(resolvedEvent("r-1", "payments-latency", now.Add(time.Minute)))
	if res.Disposition != "unmatched_resolution" || res.IncidentID != nil {
		t.Fatalf("resolution for another fingerprint: %+v", res)
	}
	if got := f.incident(*open.IncidentID).Status; got != "open" {
		t.Fatalf("unrelated incident status %s", got)
	}
	if n := f.count(`SELECT count(*) FROM operational_events WHERE id = $1 AND disposition = 'unmatched_resolution' AND incident_id IS NULL`, res.EventID); n != 1 {
		t.Fatal("unmatched resolution not stored as such")
	}
	if n := f.count(`SELECT count(*) FROM incident_events WHERE event_id = $1`, res.EventID); n != 0 {
		t.Fatal("unmatched resolution was linked")
	}
}
