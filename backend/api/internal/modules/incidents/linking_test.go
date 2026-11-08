package incidents_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/incidents"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

func deployEvent(sourceEventID string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "deployment.completed",
		"deployed_at": at.Format(time.RFC3339Nano), "external_component_id": "payment-service",
		"environment": "production", "version": "1.8.4", "commit_sha": "abc123",
		"repository": "uppsel/payments", "summary": "Deploy payment-service 1.8.4",
		"source_url": "https://deploys.example/payments/1.8.4",
	}
}

func errorObserved(sourceEventID string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "error.observed",
		"occurred_at": at.Format(time.RFC3339Nano), "external_component_id": "payments-prod",
		"environment": "production", "severity": "error", "summary": "PaymentGatewayTimeout",
		"source_url": "https://errors.example/issues/88",
	}
}

// sendDeploy posts a deployment from the deploys integration and expects 201.
func (f fixture) sendDeploy(body map[string]any) ingestResult {
	f.t.Helper()
	w := testutil.PostJSON(f.t, f.h, "/v1/ingest/"+f.s.DeployIntegrationID+"/events", f.s.DeployToken, body)
	if w.Code != http.StatusCreated {
		f.t.Fatalf("deploy %v: %d %s", body["source_event_id"], w.Code, w.Body)
	}
	return testutil.Decode[ingestResult](f.t, w)
}

// linked returns the IDs of the events linked to an incident.
func (f fixture) linked(incidentID string) map[string]string {
	f.t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT event_id::text, reason FROM incident_events WHERE incident_id = $1`, incidentID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			f.t.Fatal(err)
		}
		out[id] = reason
	}
	return out
}

func TestLateDeploymentLinksWithoutMovingStart(t *testing.T) {
	f := newFixture(t)
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	dep := f.sendDeploy(deployEvent("d-1", now.Add(-10*time.Minute)))

	if dep.Disposition != "evidence" || dep.IncidentID != nil {
		t.Fatalf("deployment result: %+v", dep)
	}
	if got := f.linked(inc)[dep.EventID]; got != "same_component_time_window" {
		t.Fatalf("late deployment link reason %q", got)
	}
	if v := f.incident(inc); !v.FirstSeenAt.Equal(now) || !v.LastSeenAt.Equal(now) {
		t.Fatalf("incident window moved: %s – %s", v.FirstSeenAt, v.LastSeenAt)
	}
	if n := f.count(`SELECT count(*) FROM operational_events WHERE id = $1 AND incident_id IS NULL AND disposition = 'evidence'`, dep.EventID); n != 1 {
		t.Fatal("evidence event outcome changed")
	}
}

func TestEventsOutsideWindowNotLinked(t *testing.T) {
	f := newFixture(t)
	at := now.Add(-20 * time.Minute) // the alert; window is [at−30m, at+15m]

	type ev struct {
		id     string
		offset time.Duration
		in     bool
	}
	edges := []ev{{"m31", -31 * time.Minute, false}, {"m30", -30 * time.Minute, true}, {"p15", 15 * time.Minute, true}, {"p16", 16 * time.Minute, false}}

	// Stored before the alert: linked by the alert (LinkWindow).
	before := map[string]string{}
	for _, e := range edges {
		before[e.id] = f.send(errorObserved("before-"+e.id, at.Add(e.offset))).EventID
	}
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", at)).IncidentID
	// Arriving after the alert: linked on arrival (LinkEvidenceEvent).
	after := map[string]string{}
	for _, e := range edges {
		after[e.id] = f.sendDeploy(deployEvent("after-"+e.id, at.Add(e.offset))).EventID
	}

	links := f.linked(inc)
	for _, e := range edges {
		for path, id := range map[string]string{"before": before[e.id], "after": after[e.id]} {
			if _, ok := links[id]; ok != e.in {
				t.Errorf("%s %s (%s): linked=%v, want %v", path, e.id, e.offset, ok, e.in)
			}
		}
	}
	if len(links) != 5 { // trigger + 2 edges × 2 paths
		t.Fatalf("links: %d", len(links))
	}
}

func TestOtherComponentNotLinked(t *testing.T) {
	f := newFixture(t)
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	other := errorObserved("e-1", now.Add(-time.Minute))
	other["external_component_id"] = "checkout-prod"
	id := f.send(other).EventID

	if _, ok := f.linked(inc)[id]; ok {
		t.Fatal("event for checkout-api linked to a payment-service incident")
	}
}

func TestEvidenceLinksToBothIncidents(t *testing.T) {
	f := newFixture(t)
	a := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	b := *f.send(alertEvent("a-2", "payments-latency", "warning", now)).IncidentID
	dep := f.sendDeploy(deployEvent("d-1", now.Add(-5*time.Minute)))

	for _, inc := range []string{a, b} {
		if got := f.linked(inc)[dep.EventID]; got != "same_component_time_window" {
			t.Fatalf("incident %s: link reason %q", inc, got)
		}
	}
}

func TestAttachGrowsWindowAndLinksStoredEvents(t *testing.T) {
	f := newFixture(t)
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now.Add(-40*time.Minute))).IncidentID
	// Outside the first window (which ends at −25m), so not linked on arrival.
	errID := f.send(errorObserved("e-1", now.Add(-20*time.Minute))).EventID
	if _, ok := f.linked(inc)[errID]; ok {
		t.Fatal("error linked before the window grew")
	}

	// The repeat alert moves last_seen_at to −10m; the window now ends at +5m.
	f.send(alertEvent("a-2", "payments-errors", "critical", now.Add(-10*time.Minute)))
	if got := f.linked(inc)[errID]; got != "same_component_time_window" {
		t.Fatalf("after attach: link reason %q", got)
	}
}

func TestLinkEvidenceEventReturnsOnlyNewIDs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	b := *f.send(alertEvent("a-2", "payments-latency", "critical", now)).IncidentID
	dep := f.sendDeploy(deployEvent("d-1", now.Add(-5*time.Minute)))
	// Undo the links made on arrival so the function can be observed directly.
	if _, err := f.pool.Exec(ctx, `DELETE FROM incident_events WHERE event_id = $1`, dep.EventID); err != nil {
		t.Fatal(err)
	}

	ev := domain.Event{
		ID: dep.EventID, OrganizationID: f.s.OrgID, ProjectID: f.s.ProjectID, IntegrationID: f.s.DeployIntegrationID,
		ComponentID: f.s.Components["payment-service"], Environment: "production",
		Type: domain.EventDeploymentCompleted, OccurredAt: now.Add(-5 * time.Minute), ReceivedAt: now,
	}
	link := func() []string {
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		ids, err := incidents.LinkEvidenceEvent(ctx, tx, ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return ids
	}

	want := []string{a, b}
	slices.Sort(want)
	if got := link(); !slices.Equal(got, want) {
		t.Fatalf("first call: %v, want %v", got, want)
	}
	if got := link(); got == nil || len(got) != 0 {
		t.Fatalf("second call: %#v, want an empty non-nil slice", got)
	}
}

// TestConcurrentAlertAndEvidenceStillLink is Review Focus 2: evidence and an
// alert for one component commit at the same moment and must still be linked.
// Without the per-component advisory lock, neither transaction sees the
// other's uncommitted row and no link is made.
func TestConcurrentAlertAndEvidenceStillLink(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	component := f.s.Components["payment-service"]

	// Transaction A stores a deployment and links it (finding no incident yet).
	txA, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(ctx)
	ev := domain.Event{
		OrganizationID: f.s.OrgID, ProjectID: f.s.ProjectID, IntegrationID: f.s.DeployIntegrationID,
		ComponentID: component, Environment: "production", Type: domain.EventDeploymentCompleted,
		SourceEventID: "d-concurrent", OccurredAt: now.Add(-5 * time.Minute), ReceivedAt: now,
	}
	if err := txA.QueryRow(ctx, `
		INSERT INTO operational_events (organization_id, project_id, integration_id, component_id, environment,
		                                type, source_event_id, occurred_at, received_at, disposition)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'evidence') RETURNING id::text`,
		ev.OrganizationID, ev.ProjectID, ev.IntegrationID, ev.ComponentID, ev.Environment,
		ev.Type, ev.SourceEventID, ev.OccurredAt, ev.ReceivedAt).Scan(&ev.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := incidents.LinkEvidenceEvent(ctx, txA, ev); err != nil || len(ids) != 0 {
		t.Fatalf("link in A: %v %v", ids, err)
	}

	// Transaction B (the alert) must wait for A.
	done := make(chan *ingestResult, 1)
	go func() {
		w := testutil.PostJSON(t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken,
			alertEvent("a-1", "payments-errors", "critical", now))
		if w.Code != http.StatusCreated {
			t.Errorf("alert: %d %s", w.Code, w.Body)
			done <- nil
			return
		}
		r := testutil.Decode[ingestResult](t, w)
		done <- &r
	}()
	waitForLockWait(t, f.pool, "%pg_advisory_xact_lock%")
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	r := <-done
	if r == nil {
		t.FailNow()
	}
	if got := f.linked(*r.IncidentID)[ev.ID]; got != "same_component_time_window" {
		t.Fatalf("concurrent deployment link reason %q", got)
	}
}
