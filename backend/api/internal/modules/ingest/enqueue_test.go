package ingest_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/ingest"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

// TestIncidentCreationEnqueuesJobAtomically: if anything fails after the job
// is enqueued, nothing from the event is kept, and a retry succeeds.
func TestIncidentCreationEnqueuesJobAtomically(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)

	restore := ingest.SetAfterEnqueue(func() error { return errors.New("injected failure") })
	w := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	restore()
	if w.Code < 500 {
		t.Fatalf("with failing hook: %d %s", w.Code, w.Body)
	}
	for _, table := range []string{"operational_events", "incidents", "analysis_jobs", "incident_events"} {
		if n := count(`SELECT count(*) FROM ` + table); n != 0 {
			t.Fatalf("%s: %d rows after rollback", table, n)
		}
	}

	w = testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	if w.Code != http.StatusCreated {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	incidentID := *testutil.Decode[ingestResponse](t, w).IncidentID
	if n := count(`SELECT count(*) FROM analysis_jobs WHERE incident_id = $1 AND status = 'pending' AND reason = 'incident_created'`, incidentID); n != 1 {
		t.Fatalf("pending incident_created jobs: %d", n)
	}
}

func TestReplayDoesNotEnqueue(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)
	testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	// Let the first job leave pending so a new enqueue would be visible.
	count(`WITH u AS (UPDATE analysis_jobs SET status = 'succeeded' RETURNING 1) SELECT count(*) FROM u`)

	if w := testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1")); w.Code != http.StatusOK {
		t.Fatalf("replay: %d", w.Code)
	}
	if n := count(`SELECT count(*) FROM analysis_jobs`); n != 1 {
		t.Fatalf("jobs after replay: %d", n)
	}
}

func TestResolutionDoesNotEnqueue(t *testing.T) {
	h, s, count := setup(t)
	path := ingestPath(s.MonitorIntegrationID)
	testutil.PostJSON(t, h, path, s.MonitorToken, alert("alert-1"))
	count(`WITH u AS (UPDATE analysis_jobs SET status = 'succeeded' RETURNING 1) SELECT count(*) FROM u`)

	resolved := alert("resolve-1")
	resolved["type"] = "alert.resolved"
	w := testutil.PostJSON(t, h, path, s.MonitorToken, resolved)
	if w.Code != http.StatusCreated || testutil.Decode[ingestResponse](t, w).Disposition != "incident_resolved" {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	if n := count(`SELECT count(*) FROM analysis_jobs`); n != 1 {
		t.Fatalf("jobs after resolution: %d", n)
	}
}

// TestLateEventEnqueuesCoalesced: new work joins the pending job if there is
// one; once that job has been leased, new work gets a fresh pending job.
func TestLateEventEnqueuesCoalesced(t *testing.T) {
	h, s, count := setup(t)
	incidentID := *testutil.Decode[ingestResponse](t,
		testutil.PostJSON(t, h, ingestPath(s.MonitorIntegrationID), s.MonitorToken, alert("alert-1"))).IncidentID

	// Pending job exists: a repeat alert and a late deployment coalesce into it.
	repeat := alert("alert-2")
	repeat["occurred_at"] = now.Add(time.Minute).Format(time.RFC3339)
	if w := testutil.PostJSON(t, h, ingestPath(s.MonitorIntegrationID), s.MonitorToken, repeat); w.Code != http.StatusCreated {
		t.Fatalf("repeat: %d %s", w.Code, w.Body)
	}
	if w := testutil.PostJSON(t, h, ingestPath(s.DeployIntegrationID), s.DeployToken, deployment("deploy-1", now.Add(-7*time.Minute))); w.Code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", w.Code, w.Body)
	}
	if n := count(`SELECT count(*) FROM analysis_jobs WHERE incident_id = $1`, incidentID); n != 1 {
		t.Fatalf("jobs while one is pending: %d, want 1", n)
	}

	// The pending job is leased; the next late event needs a new job.
	if n := count(`WITH u AS (UPDATE analysis_jobs SET status = 'running' RETURNING 1) SELECT count(*) FROM u`); n != 1 {
		t.Fatalf("leased %d jobs", n)
	}
	if w := testutil.PostJSON(t, h, ingestPath(s.DeployIntegrationID), s.DeployToken, deployment("deploy-2", now.Add(-3*time.Minute))); w.Code != http.StatusCreated {
		t.Fatalf("deploy 2: %d %s", w.Code, w.Body)
	}
	if n := count(`SELECT count(*) FROM analysis_jobs WHERE incident_id = $1 AND status = 'pending' AND reason = 'late_event'`, incidentID); n != 1 {
		t.Fatalf("pending late_event jobs: %d", n)
	}

	// Evidence that links to nothing enqueues nothing.
	other := errorEvent("err-other", now)
	other["external_component_id"] = "checkout-prod"
	testutil.PostJSON(t, h, ingestPath(s.MonitorIntegrationID), s.MonitorToken, other)
	if n := count(`SELECT count(*) FROM analysis_jobs`); n != 2 {
		t.Fatalf("total jobs: %d", n)
	}
}

// Ingest refuses a disabled integration before looking at the event, even
// for callers that skip the webhook handler's own check.
func TestIngestRefusesADisabledIntegration(t *testing.T) {
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	svc := ingest.Service{Pool: pool, Now: time.Now}
	in := ingest.Integration{ID: s.MonitorIntegrationID, OrganizationID: s.OrgID, ProjectID: s.ProjectID,
		Capability: domain.CapabilityIncidentInput, Enabled: false}
	if _, err := svc.Ingest(context.Background(), in, ingest.Envelope{}); err != ingest.ErrDisabled {
		t.Fatalf("got %v, want ErrDisabled", err)
	}
}
