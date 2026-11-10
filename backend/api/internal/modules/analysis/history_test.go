package analysis_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/modules/analysis"
	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

// newClockFixture is newFixture with a clock the test can move, so events
// can be linked after a job started.
func newClockFixture(t *testing.T) (fixture, *time.Time) {
	t.Helper()
	f := newFixture(t)
	clock := now
	f.h = server.New(server.Deps{Pool: f.pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return clock }})
	return f, &clock
}

func deployment(sourceEventID string, at time.Time) map[string]any {
	return map[string]any{
		"schema_version": "1", "source_event_id": sourceEventID, "type": "deployment.completed",
		"deployed_at": at.Format(time.RFC3339Nano), "external_component_id": "payment-service",
		"environment": "production", "version": "1.8.4", "commit_sha": "abc123",
		"repository": "uppsel/payments", "summary": "Deploy payment-service 1.8.4",
	}
}

// completeJob stands in for the worker: the job started at startedAt and
// wrote revision n with findings of the given (kind, rule_id).
func (f fixture) completeJob(jobID, incidentID string, n int, startedAt time.Time, rules ...analysis.RuleRef) {
	f.t.Helper()
	f.exec(`UPDATE analysis_jobs SET status = 'succeeded', attempts = 1, started_at = $2, finished_at = $3 WHERE id = $1`,
		jobID, startedAt, startedAt.Add(2*time.Second))
	var rev string
	if err := f.pool.QueryRow(context.Background(), `
		INSERT INTO analysis_revisions (incident_id, job_id, revision_number, created_at) VALUES ($1, $2, $3, $4)
		RETURNING id::text`, incidentID, jobID, n, startedAt.Add(2*time.Second)).Scan(&rev); err != nil {
		f.t.Fatal(err)
	}
	for i, r := range rules {
		f.exec(`INSERT INTO findings (revision_id, ordinal, kind, rule_id, text) VALUES ($1, $2, $3, $4, 'text')`, rev, i, r.Kind, r.RuleID)
	}
}

func (f fixture) pendingJob(incidentID string) (id, reason string) {
	f.t.Helper()
	if err := f.pool.QueryRow(context.Background(), `SELECT id::text, reason FROM analysis_jobs WHERE incident_id = $1 AND status = 'pending'`,
		incidentID).Scan(&id, &reason); err != nil {
		f.t.Fatal(err)
	}
	return id, reason
}

func (f fixture) history(incidentID string) []analysis.HistoryItem {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+incidentID+"/revisions", f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("revisions: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[struct {
		Items []analysis.HistoryItem `json:"items"`
	}](f.t, w).Items
}

func eventIDs(links []analysis.EvidenceLink) []string {
	ids := make([]string, len(links))
	for i, l := range links {
		ids[i] = l.EventID
	}
	slices.Sort(ids)
	return ids
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func rule(kind, id string) analysis.RuleRef { return analysis.RuleRef{Kind: kind, RuleID: id} }

// Revision 1 has no predecessor: changes is null (not "everything removed")
// and its new evidence is every link up to its job's start.
func TestRevisionHistoryFirstRevision(t *testing.T) {
	f, clock := newClockFixture(t)
	deployID, _ := f.ingest(f.s.DeployIntegrationID, f.s.DeployToken, deployment("deploy-1", now.Add(-7*time.Minute)))
	incidentID, alertID := f.openIncident() // links the stored deployment too
	job, _ := f.pendingJob(incidentID)

	*clock = now.Add(time.Minute)
	f.completeJob(job, incidentID, 1, now.Add(30*time.Second), rule("observation", "trigger_alert"), rule("next_check", "inspect_telemetry"))

	// The raw JSON must carry "changes": null.
	w := testutil.GetJSON(t, f.h, "/v1/incidents/"+incidentID+"/revisions", f.s.AdminToken)
	raw := testutil.Decode[struct {
		Items []map[string]any `json:"items"`
	}](t, w).Items
	if len(raw) != 1 {
		t.Fatalf("items: %v", raw)
	}
	if c, ok := raw[0]["changes"]; !ok || c != nil {
		t.Fatalf("revision 1 changes = %v (present %v), want null", c, ok)
	}

	h := f.history(incidentID)
	r := h[0]
	if r.RevisionNumber != 1 || r.Reason != analysis.ReasonIncidentCreated || r.FindingCount != 2 || r.Changes != nil || r.CreatedAt.IsZero() {
		t.Fatalf("revision 1: %+v", r)
	}
	if got, want := eventIDs(r.NewEvidence), sorted(deployID, alertID); !slices.Equal(got, want) {
		t.Fatalf("new evidence %v, want %v", got, want)
	}
	for _, l := range r.NewEvidence {
		if l.Type == "" || l.ReceivedAt.IsZero() {
			t.Fatalf("evidence link missing fields: %+v", l)
		}
	}
}

// The design's reference story: the alert is analysed with no deployment
// data, then the deployment arrives late and revision 2 adds what it
// supports and drops what it answers.
func TestRevisionHistoryLateDeployment(t *testing.T) {
	f, clock := newClockFixture(t)
	incidentID, alertID := f.openIncident()
	job1, _ := f.pendingJob(incidentID)
	f.completeJob(job1, incidentID, 1, now.Add(30*time.Second),
		rule("observation", "trigger_alert"),
		rule("unknown", "no_deployment_evidence"),
		rule("unknown", "no_error_evidence"),
		rule("next_check", "check_change_history"),
		rule("next_check", "inspect_telemetry"))

	*clock = now.Add(5 * time.Minute)
	deployID, _ := f.ingest(f.s.DeployIntegrationID, f.s.DeployToken, deployment("deploy-late", now.Add(-7*time.Minute)))
	job2, reason := f.pendingJob(incidentID)
	if reason != analysis.ReasonLateEvent {
		t.Fatalf("late deployment enqueued %q", reason)
	}
	f.completeJob(job2, incidentID, 2, now.Add(6*time.Minute),
		rule("observation", "trigger_alert"),
		rule("observation", "deployment_in_window"),
		rule("hypothesis", "deployment_near_onset"),
		rule("unknown", "no_error_evidence"),
		rule("next_check", "compare_by_version"),
		rule("next_check", "review_change"),
		rule("next_check", "inspect_telemetry"))

	// Linked after job 2 started: no revision has seen it yet.
	*clock = now.Add(7 * time.Minute)
	f.ingest(f.s.DeployIntegrationID, f.s.DeployToken, deployment("deploy-later", now.Add(-3*time.Minute)))

	h := f.history(incidentID)
	if len(h) != 2 || h[0].RevisionNumber != 2 || h[1].RevisionNumber != 1 {
		t.Fatalf("history not newest first: %+v", h)
	}
	r2 := h[0]
	if r2.Reason != analysis.ReasonLateEvent || r2.FindingCount != 7 || r2.Changes == nil {
		t.Fatalf("revision 2: %+v", r2)
	}
	wantAdded := []analysis.RuleRef{rule("observation", "deployment_in_window"), rule("hypothesis", "deployment_near_onset"),
		rule("next_check", "compare_by_version"), rule("next_check", "review_change")}
	wantRemoved := []analysis.RuleRef{rule("unknown", "no_deployment_evidence"), rule("next_check", "check_change_history")}
	if !slices.Equal(r2.Changes.Added, wantAdded) || !slices.Equal(r2.Changes.Removed, wantRemoved) {
		t.Fatalf("changes: %+v", r2.Changes)
	}
	if got := eventIDs(r2.NewEvidence); !slices.Equal(got, []string{deployID}) || r2.NewEvidence[0].Type != "deployment.completed" {
		t.Fatalf("revision 2 new evidence: %+v", r2.NewEvidence)
	}
	if got := eventIDs(h[1].NewEvidence); !slices.Equal(got, []string{alertID}) || h[1].Changes != nil {
		t.Fatalf("revision 1: %+v", h[1])
	}

	// The detail view counts both revisions and shows the newest job's times.
	a := f.detailAnalysis(incidentID)
	if a.RevisionCount != 2 {
		t.Fatalf("revision_count %d", a.RevisionCount)
	}
}

func TestRevisionByNumber404(t *testing.T) {
	f := newFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	incidentID, _ := f.openIncident()
	job, _ := f.pendingJob(incidentID)
	f.completeJob(job, incidentID, 1, now, rule("observation", "trigger_alert"), rule("next_check", "inspect_telemetry"))

	base := "/v1/incidents/" + incidentID + "/revisions"
	w := testutil.GetJSON(t, f.h, base+"/1", f.s.AdminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("revision 1: %d %s", w.Code, w.Body)
	}
	rev := testutil.Decode[analysis.Revision](t, w)
	if rev.RevisionNumber != 1 || rev.ID == "" || len(rev.Findings) != 2 || rev.Findings[1].RuleID != "inspect_telemetry" {
		t.Fatalf("revision 1: %+v", rev)
	}

	for name, c := range map[string]struct{ path, token string }{
		"missing revision":      {base + "/2", f.s.AdminToken},
		"zero":                  {base + "/0", f.s.AdminToken},
		"not a number":          {base + "/one", f.s.AdminToken},
		"other org revision":    {base + "/1", other.AdminToken},
		"other org history":     {base, other.AdminToken},
		"unknown incident":      {"/v1/incidents/2dd80324-2e0e-421e-95f0-f848067c4124/revisions", f.s.AdminToken},
		"unknown incident rev":  {"/v1/incidents/2dd80324-2e0e-421e-95f0-f848067c4124/revisions/1", f.s.AdminToken},
		"malformed incident id": {"/v1/incidents/nope/revisions/1", f.s.AdminToken},
	} {
		w := testutil.GetJSON(t, f.h, c.path, c.token)
		if w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestHistoryWithoutRevisions(t *testing.T) {
	f := newFixture(t)
	incidentID, _ := f.openIncident()
	if h := f.history(incidentID); len(h) != 0 {
		t.Fatalf("history: %+v", h)
	}
	w := testutil.GetJSON(t, f.h, "/v1/incidents/"+incidentID+"/revisions", f.s.AdminToken)
	if body := w.Body.String(); body != "{\"items\":[]}\n" {
		t.Fatalf("body %q", body)
	}
}

type timedView struct {
	Status        string     `json:"status"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	RevisionCount int        `json:"revision_count"`
}

func (f fixture) detailAnalysis(incidentID string) timedView {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+incidentID, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("get incident: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[struct {
		Analysis timedView `json:"analysis"`
	}](f.t, w).Analysis
}

func TestDetailShowsJobTimes(t *testing.T) {
	f := newFixture(t)
	incidentID, _ := f.openIncident()
	a := f.detailAnalysis(incidentID)
	if a.Status != "pending" || a.StartedAt != nil || a.FinishedAt != nil || a.RevisionCount != 0 {
		t.Fatalf("pending: %+v", a)
	}

	job, _ := f.pendingJob(incidentID)
	started := now.Add(time.Second)
	f.exec(`UPDATE analysis_jobs SET status = 'running', started_at = $2 WHERE id = $1`, job, started)
	a = f.detailAnalysis(incidentID)
	if a.Status != "running" || a.StartedAt == nil || !a.StartedAt.Equal(started) || a.FinishedAt != nil {
		t.Fatalf("running: %+v", a)
	}

	f.completeJob(job, incidentID, 1, started)
	a = f.detailAnalysis(incidentID)
	if a.Status != "succeeded" || !a.StartedAt.Equal(started) || a.FinishedAt == nil || !a.FinishedAt.Equal(started.Add(2*time.Second)) || a.RevisionCount != 1 {
		t.Fatalf("succeeded: %+v", a)
	}
}
