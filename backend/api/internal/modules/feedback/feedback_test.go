package feedback_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

var now = time.Date(2026, 11, 8, 14, 9, 0, 0, time.UTC)

type fixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	h        http.Handler
	s        seed.Result
	incident string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := testutil.NewDB(t)
	s, err := seed.Seed(context.Background(), pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }})
	f := fixture{t: t, pool: pool, h: h, s: s}
	w := testutil.PostJSON(t, h, "/v1/ingest/"+s.MonitorIntegrationID+"/events", s.MonitorToken, map[string]any{
		"schema_version": "1", "source_event_id": "alert-1", "type": "alert.triggered",
		"occurred_at": now.Format(time.RFC3339), "external_component_id": "payments-prod",
		"environment": "production", "severity": "critical", "fingerprint": "payments-errors",
		"summary": "Payment errors affecting checkout",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("ingest: %d %s", w.Code, w.Body)
	}
	f.incident = *testutil.Decode[struct {
		IncidentID *string `json:"incident_id"`
	}](t, w).IncidentID
	return f
}

func (f fixture) scalar(sql string, args ...any) string {
	f.t.Helper()
	var v string
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		f.t.Fatalf("%q: %v", sql, err)
	}
	return v
}

// revision writes revision n (with a job) holding an observation and a next
// check; it returns their finding IDs.
func (f fixture) revision(n int) (observation, check string) {
	f.t.Helper()
	job := f.scalar(`INSERT INTO analysis_jobs (incident_id, reason, status) VALUES ($1, 'manual_retry', 'succeeded') RETURNING id::text`, f.incident)
	rev := f.scalar(`INSERT INTO analysis_revisions (incident_id, job_id, revision_number) VALUES ($1, $2, $3) RETURNING id::text`, f.incident, job, n)
	observation = f.scalar(`INSERT INTO findings (revision_id, ordinal, kind, rule_id, text) VALUES ($1, 0, 'observation', 'trigger_alert', 'An alert fired.') RETURNING id::text`, rev)
	check = f.scalar(`INSERT INTO findings (revision_id, ordinal, kind, rule_id, text) VALUES ($1, 1, 'next_check', 'inspect_telemetry', 'Inspect logs.') RETURNING id::text`, rev)
	return observation, check
}

// member adds a second member of the organization and returns their token.
func (f fixture) member(name string) string {
	f.t.Helper()
	user := f.scalar(`INSERT INTO users (email, display_name) VALUES ($1, $2) RETURNING id::text`, name+"@uppsel.test", name)
	f.scalar(`INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, 'member') RETURNING role`, f.s.OrgID, user)
	plain, hash := auth.NewToken(domain.UserTokenPrefix)
	f.scalar(`INSERT INTO api_tokens (user_id, token_hash) VALUES ($1, $2) RETURNING id::text`, user, hash)
	return plain
}

type feedbackBody struct {
	Useful    int     `json:"useful"`
	Incorrect int     `json:"incorrect"`
	Mine      *string `json:"mine"`
}

type findingView struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	Feedback feedbackBody `json:"feedback"`
	Check    *struct {
		Done   bool `json:"done"`
		DoneBy *struct {
			DisplayName string `json:"display_name"`
		} `json:"done_by"`
		DoneAt *time.Time `json:"done_at"`
	} `json:"check"`
}

func (f fixture) put(path, token string, body any) (int, feedbackBody) {
	f.t.Helper()
	w := testutil.SendJSON(f.t, f.h, http.MethodPut, path, token, body)
	if w.Code != http.StatusOK {
		return w.Code, feedbackBody{}
	}
	return w.Code, testutil.Decode[feedbackBody](f.t, w)
}

// latest returns the latest revision's findings as token sees them.
func (f fixture) latest(token string) []findingView {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+f.incident, token)
	if w.Code != http.StatusOK {
		f.t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[struct {
		Analysis struct {
			LatestRevision struct {
				Findings []findingView `json:"findings"`
			} `json:"latest_revision"`
		} `json:"analysis"`
	}](f.t, w).Analysis.LatestRevision.Findings
}

func mine(v *string) string {
	if v == nil {
		return "<nil>"
	}
	return *v
}

func TestFeedbackSetChangeClear(t *testing.T) {
	f := newFixture(t)
	obs, _ := f.revision(1)
	path := "/v1/findings/" + obs + "/feedback"

	if code, fb := f.put(path, f.s.AdminToken, map[string]string{"verdict": "useful"}); code != 200 || fb.Useful != 1 || fb.Incorrect != 0 || mine(fb.Mine) != "useful" {
		t.Fatalf("set: %d %+v", code, fb)
	}
	if code, fb := f.put(path, f.s.AdminToken, map[string]string{"verdict": "incorrect"}); code != 200 || fb.Useful != 0 || fb.Incorrect != 1 || mine(fb.Mine) != "incorrect" {
		t.Fatalf("change: %d %+v", code, fb)
	}
	got := f.latest(f.s.AdminToken)[0]
	if got.ID != obs || got.Feedback.Incorrect != 1 || mine(got.Feedback.Mine) != "incorrect" || got.Check != nil {
		t.Fatalf("read model: %+v", got)
	}

	w := testutil.SendJSON(t, f.h, http.MethodDelete, path, f.s.AdminToken, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	if got := f.latest(f.s.AdminToken)[0].Feedback; got.Useful+got.Incorrect != 0 || got.Mine != nil {
		t.Fatalf("after clear: %+v", got)
	}

	for name, body := range map[string]any{
		"unknown verdict": map[string]string{"verdict": "great"},
		"empty verdict":   map[string]string{"verdict": ""},
		"no verdict":      map[string]string{},
	} {
		w := testutil.SendJSON(t, f.h, http.MethodPut, path, f.s.AdminToken, body)
		if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_verdict" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestFeedbackCounts(t *testing.T) {
	f := newFixture(t)
	obs, _ := f.revision(1)
	ama := f.member("Ama")
	path := "/v1/findings/" + obs + "/feedback"
	f.put(path, f.s.AdminToken, map[string]string{"verdict": "useful"})
	if _, fb := f.put(path, ama, map[string]string{"verdict": "incorrect"}); fb.Useful != 1 || fb.Incorrect != 1 || mine(fb.Mine) != "incorrect" {
		t.Fatalf("second user: %+v", fb)
	}
	if fb := f.latest(f.s.AdminToken)[0].Feedback; fb.Useful != 1 || fb.Incorrect != 1 || mine(fb.Mine) != "useful" {
		t.Fatalf("admin view: %+v", fb)
	}
	// The other finding in the revision has no feedback of its own.
	if fb := f.latest(f.s.AdminToken)[1].Feedback; fb.Useful != 0 || fb.Incorrect != 0 || fb.Mine != nil {
		t.Fatalf("other finding: %+v", fb)
	}
	if fb := f.latest(ama)[0].Feedback; mine(fb.Mine) != "incorrect" {
		t.Fatalf("member view: %+v", fb)
	}
}

func TestFeedbackOtherOrg404(t *testing.T) {
	f := newFixture(t)
	obs, check := f.revision(1)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, path, token string
		body                      any
	}{
		{"feedback other org", http.MethodPut, "/v1/findings/" + obs + "/feedback", other.AdminToken, map[string]string{"verdict": "useful"}},
		{"clear other org", http.MethodDelete, "/v1/findings/" + obs + "/feedback", other.AdminToken, nil},
		{"check other org", http.MethodPut, "/v1/findings/" + check + "/check", other.AdminToken, map[string]bool{"done": true}},
		{"unknown finding", http.MethodPut, "/v1/findings/2dd80324-2e0e-421e-95f0-f848067c4124/feedback", f.s.AdminToken, map[string]string{"verdict": "useful"}},
		{"malformed id", http.MethodPut, "/v1/findings/nope/check", f.s.AdminToken, map[string]bool{"done": true}},
	}
	for _, c := range cases {
		w := testutil.SendJSON(t, f.h, c.method, c.path, c.token, c.body)
		if w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	if n := f.scalar(`SELECT (SELECT count(*) FROM finding_feedback) + (SELECT count(*) FROM next_check_status)`); n != "0" {
		t.Fatalf("other org wrote %s rows", n)
	}
}

func TestCheckOnlyNextCheck(t *testing.T) {
	f := newFixture(t)
	obs, check := f.revision(1)
	ama := f.member("Ama")

	w := testutil.SendJSON(t, f.h, http.MethodPut, "/v1/findings/"+obs+"/check", f.s.AdminToken, map[string]bool{"done": true})
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "not_a_check" {
		t.Fatalf("observation: %d %s", w.Code, w.Body)
	}
	w = testutil.SendJSON(t, f.h, http.MethodPut, "/v1/findings/"+check+"/check", f.s.AdminToken, map[string]any{})
	if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "missing_field" {
		t.Fatalf("no done: %d %s", w.Code, w.Body)
	}

	w = testutil.SendJSON(t, f.h, http.MethodPut, "/v1/findings/"+check+"/check", ama, map[string]bool{"done": true})
	if w.Code != http.StatusOK {
		t.Fatalf("done: %d %s", w.Code, w.Body)
	}
	// Marking it again keeps the first person's credit.
	testutil.SendJSON(t, f.h, http.MethodPut, "/v1/findings/"+check+"/check", f.s.AdminToken, map[string]bool{"done": true})
	c := f.latest(f.s.AdminToken)[1].Check
	if c == nil || !c.Done || c.DoneBy == nil || c.DoneBy.DisplayName != "Ama" || c.DoneAt == nil || !c.DoneAt.Equal(now) {
		t.Fatalf("done check: %+v", c)
	}

	w = testutil.SendJSON(t, f.h, http.MethodPut, "/v1/findings/"+check+"/check", f.s.AdminToken, map[string]bool{"done": false})
	if w.Code != http.StatusOK {
		t.Fatalf("undo: %d", w.Code)
	}
	if c := f.latest(f.s.AdminToken)[1].Check; c == nil || c.Done || c.DoneBy != nil || c.DoneAt != nil {
		t.Fatalf("undone: %+v", c)
	}
}

// Review Focus 5: feedback on a superseded revision's finding is accepted
// and shown with that revision, but the new revision's findings start clean.
func TestFeedbackDoesNotCarryAcrossRevisions(t *testing.T) {
	f := newFixture(t)
	oldObs, oldCheck := f.revision(1)
	f.revision(2)
	if code, _ := f.put("/v1/findings/"+oldObs+"/feedback", f.s.AdminToken, map[string]string{"verdict": "incorrect"}); code != 200 {
		t.Fatalf("feedback on old revision: %d", code)
	}
	testutil.SendJSON(t, f.h, http.MethodPut, "/v1/findings/"+oldCheck+"/check", f.s.AdminToken, map[string]bool{"done": true})

	latest := f.latest(f.s.AdminToken)
	if latest[0].Feedback.Incorrect != 0 || latest[0].Feedback.Mine != nil || latest[1].Check.Done {
		t.Fatalf("carried over: %+v %+v", latest[0].Feedback, latest[1].Check)
	}
	w := testutil.GetJSON(t, f.h, "/v1/incidents/"+f.incident+"/revisions/1", f.s.AdminToken)
	old := testutil.Decode[struct {
		Findings []findingView `json:"findings"`
	}](t, w).Findings
	if mine(old[0].Feedback.Mine) != "incorrect" || !old[1].Check.Done {
		t.Fatalf("revision 1: %+v %+v", old[0].Feedback, old[1].Check)
	}
}
