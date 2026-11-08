package analysis_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

func TestReanalyze(t *testing.T) {
	f := newFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	incidentID, _ := f.openIncident()
	path := "/v1/incidents/" + incidentID + "/reanalyze"
	reanalyze := func(token string) (int, string) {
		w := testutil.PostJSON(t, f.h, path, token, map[string]any{})
		if w.Code != http.StatusAccepted {
			return w.Code, ""
		}
		return w.Code, testutil.Decode[struct {
			JobID string `json:"job_id"`
		}](t, w).JobID
	}

	// A pending job already exists: that job is returned.
	var pending string
	if err := f.pool.QueryRow(context.Background(), `SELECT id::text FROM analysis_jobs`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if code, id := reanalyze(f.s.AdminToken); code != http.StatusAccepted || id != pending {
		t.Fatalf("with pending: %d %s, want %s", code, id, pending)
	}

	// After a failed job, a fresh manual_retry job is created.
	f.exec(`UPDATE analysis_jobs SET status = 'failed', attempts = 5, last_error = 'boom' WHERE id = $1`, pending)
	code, id := reanalyze(f.s.AdminToken)
	if code != http.StatusAccepted || id == "" || id == pending {
		t.Fatalf("after failure: %d %s", code, id)
	}
	if n := f.count(`SELECT count(*) FROM analysis_jobs WHERE id = $1 AND status = 'pending' AND reason = 'manual_retry' AND attempts = 0`, id); n != 1 {
		t.Fatal("manual_retry job not created fresh")
	}

	for name, c := range map[string]struct{ path, token string }{
		"other org": {path, other.AdminToken},
		"unknown":   {"/v1/incidents/2dd80324-2e0e-421e-95f0-f848067c4124/reanalyze", f.s.AdminToken},
		"malformed": {"/v1/incidents/nope/reanalyze", f.s.AdminToken},
	} {
		w := testutil.PostJSON(t, f.h, c.path, c.token, map[string]any{})
		if w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if n := f.count(`SELECT count(*) FROM analysis_jobs`); n != 2 {
		t.Fatalf("jobs: %d", n)
	}
}

type analysisView struct {
	Status         string  `json:"status"`
	LastError      *string `json:"last_error"`
	LatestRevision *struct {
		ID             string    `json:"id"`
		RevisionNumber int       `json:"revision_number"`
		CreatedAt      time.Time `json:"created_at"`
		Findings       []struct {
			Kind        string          `json:"kind"`
			RuleID      string          `json:"rule_id"`
			Text        string          `json:"text"`
			Limitations *string         `json:"limitations"`
			EvidenceIDs json.RawMessage `json:"evidence_ids"`
		} `json:"findings"`
	} `json:"latest_revision"`
}

func (f fixture) analysis(incidentID string) analysisView {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+incidentID, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("get incident: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[struct {
		Analysis analysisView `json:"analysis"`
	}](f.t, w).Analysis
}

func TestIncidentDetailShowsAnalysis(t *testing.T) {
	f := newFixture(t)
	incidentID, triggerID := f.openIncident()

	a := f.analysis(incidentID)
	if a.Status != "pending" || a.LastError != nil || a.LatestRevision != nil {
		t.Fatalf("new incident: %+v", a)
	}

	// Revision 1 from the first job, revision 2 from a second one; the
	// latest revision is shown, with findings in ordinal order.
	var job1, job2, rev1, rev2 string
	if err := f.pool.QueryRow(context.Background(), `UPDATE analysis_jobs SET status = 'succeeded' RETURNING id::text`).Scan(&job1); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO analysis_revisions (incident_id, job_id, revision_number) VALUES ($1, $2, 1) RETURNING id::text`, incidentID, job1).Scan(&rev1); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO analysis_jobs (incident_id, reason, status) VALUES ($1, 'late_event', 'succeeded') RETURNING id::text`, incidentID).Scan(&job2); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO analysis_revisions (incident_id, job_id, revision_number) VALUES ($1, $2, 2) RETURNING id::text`, incidentID, job2).Scan(&rev2); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO findings (revision_id, ordinal, kind, rule_id, text) VALUES ($1, 0, 'observation', 'old', 'old')`, rev1)
	var obs, next string
	// Inserted out of order to prove ordering by ordinal.
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO findings (revision_id, ordinal, kind, rule_id, text) VALUES ($1, 1, 'next_check', 'inspect_telemetry', 'Inspect logs.') RETURNING id::text`, rev2).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO findings (revision_id, ordinal, kind, rule_id, text, limitations) VALUES ($1, 0, 'hypothesis', 'deployment_near_onset', 'May be related.', 'Timing alone cannot establish causation.') RETURNING id::text`, rev2).Scan(&obs); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO finding_evidence (finding_id, event_id) VALUES ($1, $2)`, obs, triggerID)

	a = f.analysis(incidentID)
	if a.Status != "succeeded" || a.LastError != nil || a.LatestRevision == nil {
		t.Fatalf("after revisions: %+v", a)
	}
	r := a.LatestRevision
	if r.ID != rev2 || r.RevisionNumber != 2 || r.CreatedAt.IsZero() || len(r.Findings) != 2 {
		t.Fatalf("latest revision: %+v", r)
	}
	h, n := r.Findings[0], r.Findings[1]
	if h.Kind != "hypothesis" || h.RuleID != "deployment_near_onset" || h.Limitations == nil || string(h.EvidenceIDs) != `["`+triggerID+`"]` {
		t.Fatalf("first finding: %+v %s", h, h.EvidenceIDs)
	}
	if n.Kind != "next_check" || n.Limitations != nil || string(n.EvidenceIDs) != `[]` {
		t.Fatalf("second finding: %+v evidence_ids=%s", n, n.EvidenceIDs)
	}

	// The newest job decides status and last_error; the revision stays.
	f.exec(`INSERT INTO analysis_jobs (incident_id, reason, status, attempts, last_error) VALUES ($1, 'manual_retry', 'failed', 5, 'RuntimeError: boom')`, incidentID)
	a = f.analysis(incidentID)
	if a.Status != "failed" || a.LastError == nil || *a.LastError != "RuntimeError: boom" || a.LatestRevision == nil || a.LatestRevision.ID != rev2 {
		t.Fatalf("after failure: %+v", a)
	}
}
