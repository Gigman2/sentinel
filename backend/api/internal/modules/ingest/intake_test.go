package ingest_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

func deployment(sourceEventID string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "deployment.completed",
		"deployed_at": at.Format(time.RFC3339), "external_component_id": "payment-service",
		"environment": "production", "version": "1.8.4", "commit_sha": "abc123",
		"repository": "uppsel/payments", "summary": "Deploy payment-service 1.8.4",
	}
}

func errorEvent(sourceEventID string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "error.observed",
		"occurred_at": at.Format(time.RFC3339), "external_component_id": "payments-prod",
		"environment": "production", "severity": "error", "summary": "PaymentGatewayTimeout",
		"attributes": map[string]any{"count": 42, "api_key": "should-not-be-stored"},
	}
}

func TestReplayReturnsOriginal(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)

	first := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	if first.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	again := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	if again.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", again.Code, again.Body)
	}
	a, b := testutil.Decode[ingestResponse](t, first), testutil.Decode[ingestResponse](t, again)
	if !b.Replayed || b.EventID != a.EventID || b.Disposition != a.Disposition || b.IncidentID == nil || *b.IncidentID != *a.IncidentID {
		t.Fatalf("replay %+v does not match original %+v", b, a)
	}
	if n := count(`SELECT count(*) FROM operational_events`); n != 1 {
		t.Fatalf("events: %d", n)
	}
	if n := count(`SELECT count(*) FROM incidents`); n != 1 {
		t.Fatalf("incidents: %d", n)
	}

	// An evidence event replays with its own stored outcome.
	dpath := ingestPath(s.DeployIntegrationID)
	d1 := testutil.Decode[ingestResponse](t, testutil.PostJSON(t, h, dpath, s.DeployToken, deployment("deploy-1", now.Add(-7*time.Minute))))
	w := testutil.PostJSON(t, h, dpath, s.DeployToken, deployment("deploy-1", now.Add(-7*time.Minute)))
	d2 := testutil.Decode[ingestResponse](t, w)
	if w.Code != http.StatusOK || !d2.Replayed || d2.EventID != d1.EventID || d2.Disposition != "evidence" || d2.IncidentID != nil {
		t.Fatalf("evidence replay: %d %+v", w.Code, d2)
	}
}

// Review Focus 1: a resend with a changed body returns the original, unchanged.
func TestReplayWithDifferentBodyIgnored(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)
	first := testutil.Decode[ingestResponse](t, testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1")))

	changed := alert("alert-1")
	changed["summary"], changed["severity"] = "changed", "info"
	w := testutil.PostJSON(t, h, path, s.MonitorToken, changed)
	if got := testutil.Decode[ingestResponse](t, w); w.Code != http.StatusOK || !got.Replayed || got.EventID != first.EventID {
		t.Fatalf("changed replay: %d %+v", w.Code, got)
	}
	if n := count(`SELECT count(*) FROM operational_events WHERE summary = 'Checkout error rate exceeded threshold' AND severity = 'critical'`); n != 1 {
		t.Fatal("stored event was modified by the replay")
	}
	if n := count(`SELECT count(*) FROM incidents WHERE severity = 'critical' AND title = 'Checkout error rate exceeded threshold'`); n != 1 {
		t.Fatal("incident was modified by the replay")
	}
}

// A replay is answered from the stored event, even after the original
// timestamp has aged past the 30-day limit.
func TestReplayOfAgedEventStillReturnsOriginal(t *testing.T) {
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	h := server.New(server.Deps{Pool: pool, Now: func() time.Time { return clock }})
	path := ingestPath(s.MonitorIntegrationID)

	first := testutil.Decode[ingestResponse](t, testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1")))
	clock = now.Add(40 * 24 * time.Hour)
	w := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	if got := testutil.Decode[ingestResponse](t, w); w.Code != http.StatusOK || got.EventID != first.EventID {
		t.Fatalf("aged replay: %d %s", w.Code, w.Body)
	}
}

func TestConcurrentReplay(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)

	const n = 20
	codes := make([]int, n)
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-same"))
			codes[i] = w.Code
			if w.Code < 300 {
				ids[i] = testutil.Decode[ingestResponse](t, w).EventID
			}
		}(i)
	}
	wg.Wait()

	created, replayed := 0, 0
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			replayed++
		default:
			t.Fatalf("request %d: unexpected status %d", i, c)
		}
		if ids[i] != ids[0] {
			t.Fatalf("different event IDs returned: %s vs %s", ids[i], ids[0])
		}
	}
	if created != 1 || replayed != n-1 {
		t.Fatalf("created=%d replayed=%d", created, replayed)
	}
	if got := count(`SELECT count(*) FROM operational_events`); got != 1 {
		t.Fatalf("events: %d", got)
	}
}

func TestUnmappedComponentRejected(t *testing.T) {
	h, s, count := setup(t)
	body := alert("alert-ghost")
	body["external_component_id"] = "ghost"
	w := testutil.PostJSON(t, h, ingestPath(s.MonitorIntegrationID), s.MonitorToken, body)
	if w.Code != http.StatusUnprocessableEntity || testutil.ErrorCode(t, w) != "unmapped_component" {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	want := "no mapping for external_component_id 'ghost' in environment 'production'; add one via POST /v1/integrations/" + s.MonitorIntegrationID + "/mappings"
	if msg := testutil.Decode[testutil.ErrorBody](t, w).Error.Message; msg != want {
		t.Fatalf("message %q", msg)
	}
	if n := count(`SELECT count(*) FROM operational_events`); n != 0 {
		t.Fatalf("events: %d", n)
	}
	if n := count(`SELECT count(*) FROM intake_rejections WHERE integration_id = $1 AND organization_id = $2 AND code = 'unmapped_component'
		AND source_event_id = 'alert-ghost' AND external_component_id = 'ghost' AND environment = 'production' AND received_at = $3`,
		s.MonitorIntegrationID, s.OrgID, now); n != 1 {
		t.Fatalf("rejection records: %d", n)
	}
}

// Review Focus 5: a credential only works for its own integration.
func TestWrongIntegrationCredential(t *testing.T) {
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
	h := server.New(server.Deps{Pool: pool, Now: func() time.Time { return now }})

	for name, path := range map[string]string{
		"same org, other integration": ingestPath(a.DeployIntegrationID),
		"other org's integration":     ingestPath(b.MonitorIntegrationID),
	} {
		w := testutil.PostJSON(t, h, path, a.MonitorToken, alert("alert-"+name))
		if w.Code != http.StatusUnauthorized || testutil.ErrorCode(t, w) != "invalid_credential" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	var events, rejections int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM operational_events), (SELECT count(*) FROM intake_rejections)`).Scan(&events, &rejections); err != nil {
		t.Fatal(err)
	}
	if events != 0 || rejections != 0 {
		t.Fatalf("stored %d events and %d rejections for unauthenticated requests", events, rejections)
	}
}

func TestCapabilityEnforced(t *testing.T) {
	h, s, count := setup(t)

	// change_source may not send alerts.
	body := alert("alert-from-deploys")
	body["external_component_id"] = "payment-service"
	w := testutil.PostJSON(t, h, ingestPath(s.DeployIntegrationID), s.DeployToken, body)
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "capability_not_allowed" {
		t.Fatalf("alert via change_source: %d %s", w.Code, w.Body)
	}
	// incident_input may not send deployments.
	d := deployment("deploy-from-monitor", now.Add(-time.Minute))
	d["external_component_id"] = "payments-prod"
	w = testutil.PostJSON(t, h, ingestPath(s.MonitorIntegrationID), s.MonitorToken, d)
	if w.Code != http.StatusForbidden || testutil.ErrorCode(t, w) != "capability_not_allowed" {
		t.Fatalf("deployment via incident_input: %d %s", w.Code, w.Body)
	}
	if n := count(`SELECT count(*) FROM operational_events`); n != 0 {
		t.Fatalf("events: %d", n)
	}
	if n := count(`SELECT count(*) FROM intake_rejections WHERE code = 'capability_not_allowed'`); n != 2 {
		t.Fatalf("rejection records: %d", n)
	}
}

func TestEvidenceEventsDoNotOpenIncidents(t *testing.T) {
	h, s, count := setup(t)

	cases := []struct {
		name, path, token string
		body              map[string]any
		disposition       string
	}{
		{"deployment", ingestPath(s.DeployIntegrationID), s.DeployToken, deployment("deploy-1", now.Add(-7*time.Minute)), "evidence"},
		{"error", ingestPath(s.MonitorIntegrationID), s.MonitorToken, errorEvent("err-1", now.Add(-2*time.Minute)), "evidence"},
		{"unmatched resolution", ingestPath(s.MonitorIntegrationID), s.MonitorToken, map[string]any{
			"schema_version": "1", "source_event_id": "resolve-1", "type": "alert.resolved",
			"occurred_at": now.Format(time.RFC3339), "external_component_id": "payments-prod",
			"environment": "production", "severity": "info", "fingerprint": "nothing-open",
		}, "unmatched_resolution"},
	}
	for _, c := range cases {
		w := testutil.PostJSON(t, h, c.path, c.token, c.body)
		got := testutil.Decode[ingestResponse](t, w)
		if w.Code != http.StatusCreated || got.Disposition != c.disposition || got.IncidentID != nil {
			t.Fatalf("%s: %d %+v", c.name, w.Code, got)
		}
	}
	if n := count(`SELECT count(*) FROM incidents`); n != 0 {
		t.Fatalf("incidents: %d", n)
	}

	// Deployment fields land in details; deployed_at stands in for occurred_at.
	if n := count(`SELECT count(*) FROM operational_events WHERE source_event_id = 'deploy-1' AND occurred_at = $1
		AND details->>'version' = '1.8.4' AND details->>'commit_sha' = 'abc123' AND details->>'repository' = 'uppsel/payments'`,
		now.Add(-7*time.Minute)); n != 1 {
		t.Fatal("deployment details not stored as expected")
	}
	// Sensitive attribute keys are dropped before storage.
	if n := count(`SELECT count(*) FROM operational_events WHERE source_event_id = 'err-1' AND attributes = '{"count": 42}'::jsonb`); n != 1 {
		t.Fatal("error attributes not sanitized")
	}
}

func TestRateLimited(t *testing.T) {
	h, s, _ := setupWith(t, server.Deps{IngestRate: 0.001, IngestBurst: 2})
	path := ingestPath(s.MonitorIntegrationID)

	for i, want := range []int{201, 201, 429} {
		w := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-rl-"+string(rune('a'+i))))
		if w.Code != want {
			t.Fatalf("request %d: %d %s", i+1, w.Code, w.Body)
		}
		if want == 429 {
			if testutil.ErrorCode(t, w) != "rate_limited" || w.Header().Get("Retry-After") != "1" {
				t.Fatalf("429 response: %s (Retry-After %q)", w.Body, w.Header().Get("Retry-After"))
			}
		}
	}
	// Another integration has its own budget.
	if w := testutil.PostJSON(t, h, ingestPath(s.DeployIntegrationID), s.DeployToken, deployment("deploy-rl", now.Add(-time.Minute))); w.Code != http.StatusCreated {
		t.Fatalf("other integration: %d %s", w.Code, w.Body)
	}
}
