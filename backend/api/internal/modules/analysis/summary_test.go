package analysis_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/modules/analysis"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

func TestViewExposesStepAndSummary(t *testing.T) {
	f := newFixture(t)
	incidentID, alertID := f.openIncident()
	job, _ := f.pendingJob(incidentID)

	get := func() struct {
		Step           *string            `json:"step"`
		StepStartedAt  *time.Time         `json:"step_started_at"`
		LatestRevision *analysis.Revision `json:"latest_revision"`
	} {
		w := testutil.GetJSON(t, f.h, "/v1/incidents/"+incidentID, f.s.AdminToken)
		return testutil.Decode[struct {
			Analysis struct {
				Step           *string            `json:"step"`
				StepStartedAt  *time.Time         `json:"step_started_at"`
				LatestRevision *analysis.Revision `json:"latest_revision"`
			} `json:"analysis"`
		}](t, w).Analysis
	}
	if a := get(); a.Step != nil || a.StepStartedAt != nil {
		t.Fatalf("queued job has a step: %+v", a)
	}

	stepAt := now.Add(3 * time.Second)
	f.exec(`UPDATE analysis_jobs SET status = 'running', started_at = $2, step = 'draft_findings', step_started_at = $3 WHERE id = $1`, job, now, stepAt)
	if a := get(); a.Step == nil || *a.Step != "draft_findings" || !a.StepStartedAt.Equal(stepAt) {
		t.Fatalf("running: %+v", a)
	}

	// A revision written before summaries existed has an empty one, not null.
	f.completeJob(job, incidentID, 1, now, []string{alertID}, rule("observation", "trigger_alert"))
	if a := get(); a.LatestRevision == nil || a.LatestRevision.Summary == nil || len(a.LatestRevision.Summary) != 0 {
		t.Fatalf("legacy summary: %+v", a.LatestRevision)
	}

	f.exec(`UPDATE analysis_revisions SET summary = '[{"text":"Alert at ","kind":"text"},{"text":"14:09","kind":"time"},{"text":"Cause: Unknown.","kind":"strong"}]' WHERE incident_id = $1`, incidentID)
	want := []analysis.Segment{{Text: "Alert at ", Kind: "text"}, {Text: "14:09", Kind: "time"}, {Text: "Cause: Unknown.", Kind: "strong"}}
	a := get()
	if len(a.LatestRevision.Summary) != 3 || a.LatestRevision.Summary[1] != want[1] || a.LatestRevision.Summary[2] != want[2] {
		t.Fatalf("summary: %+v", a.LatestRevision.Summary)
	}
	w := testutil.GetJSON(t, f.h, "/v1/incidents/"+incidentID+"/revisions/1", f.s.AdminToken)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if rev := testutil.Decode[analysis.Revision](t, w); len(rev.Summary) != 3 || rev.Summary[0] != want[0] {
		t.Fatalf("revision 1 summary: %+v", rev.Summary)
	}
}
