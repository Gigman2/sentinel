package incidents_test

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

type listItem struct {
	ID             string  `json:"id"`
	Number         int64   `json:"number"`
	Status         string  `json:"status"`
	Severity       string  `json:"severity"`
	Title          string  `json:"title"`
	AnalysisStatus string  `json:"analysis_status"`
	SignalCount    int     `json:"signal_count"`
	ResolutionNote *string `json:"resolution_note"`
	Component      struct {
		Slug string `json:"slug"`
	} `json:"component"`
}

type listPage struct {
	Items      []listItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
	OpenCount  int        `json:"open_count"`
}

func (f fixture) list(query string) listPage {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/projects/"+f.s.ProjectID+"/incidents?"+query, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("list %q: %d %s", query, w.Code, w.Body)
	}
	return testutil.Decode[listPage](f.t, w)
}

func titles(p listPage) string {
	out := make([]string, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it.Title)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestListFilters(t *testing.T) {
	f := newFixture(t)
	alert := func(id, fp, severity, summary, component string, at time.Time) string {
		body := alertEvent(id, fp, severity, at)
		body["summary"], body["external_component_id"] = summary, component
		return *f.send(body).IncidentID
	}
	alert("a", "fp-a", "critical", "Payment ERRORS rising", "payments-prod", now.Add(-48*time.Hour))
	alert("b", "fp-b", "warning", "Checkout latency", "checkout-prod", now.Add(-2*time.Hour))
	c := alert("c", "fp-c", "critical", "Card errors 50% of_requests", "payments-prod", now.Add(-time.Minute))
	if w := testutil.PostJSON(t, f.h, "/v1/incidents/"+c+"/resolve", f.s.AdminToken, map[string]any{"note": "bad deploy"}); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}

	since := url.QueryEscape(now.Add(-3 * time.Hour).Format(time.RFC3339))
	cases := map[string]string{
		"":                               "Card errors 50% of_requests,Checkout latency,Payment ERRORS rising",
		"severity=critical":              "Card errors 50% of_requests,Payment ERRORS rising",
		"component=checkout-api":         "Checkout latency",
		"component=no-such-component":    "",
		"since=" + since:                 "Card errors 50% of_requests,Checkout latency",
		"q=errors":                       "Card errors 50% of_requests,Payment ERRORS rising",
		"q=" + url.QueryEscape("50% of"): "Card errors 50% of_requests",
		"q=" + url.QueryEscape("_"):      "Card errors 50% of_requests",
		"q=%25":                          "Card errors 50% of_requests",
		"status=open&severity=critical":  "Payment ERRORS rising",
		"severity=critical&q=card&since=" + since + "&component=payment-service": "Card errors 50% of_requests",
	}
	for query, want := range cases {
		p := f.list(query)
		if got := titles(p); got != want {
			t.Errorf("%q:\n got %q\nwant %q", query, got, want)
		}
		// open_count ignores the filters.
		if p.OpenCount != 2 {
			t.Errorf("%q: open_count %d, want 2", query, p.OpenCount)
		}
	}

	// Filters page like an unfiltered listing.
	p := f.list("severity=critical&limit=1")
	if len(p.Items) != 1 || p.NextCursor == nil || p.Items[0].Title != "Card errors 50% of_requests" {
		t.Fatalf("first filtered page: %+v", p)
	}
	p = f.list("severity=critical&limit=1&cursor=" + *p.NextCursor)
	if len(p.Items) != 1 || p.NextCursor != nil || p.Items[0].Title != "Payment ERRORS rising" {
		t.Fatalf("second filtered page: %+v", p)
	}

	base := "/v1/projects/" + f.s.ProjectID + "/incidents?"
	errs := map[string]string{
		"severity=fatal":                 "invalid_severity",
		"severity=Critical":              "invalid_severity",
		"since=yesterday":                "invalid_since",
		"since=2026-11-08":               "invalid_since",
		"q=" + strings.Repeat("x", 201):  "invalid_q",
		"q=%FF":                          "invalid_q",
		"q=%00":                          "invalid_q",
		"status=bogus&severity=fatal":    "invalid_status",
		"severity=fatal&since=yesterday": "invalid_severity",
		"q=" + strings.Repeat("é", 201) + "&limit=0": "invalid_q",
	}
	for query, code := range errs {
		w := testutil.GetJSON(t, f.h, base+query, f.s.AdminToken)
		if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != code {
			t.Errorf("%.40q: %d %s, want 400 %s", query, w.Code, w.Body, code)
		}
	}
	// 200 runes is accepted, even when it is more than 200 bytes.
	if w := testutil.GetJSON(t, f.h, base+"q="+url.QueryEscape(strings.Repeat("é", 200)), f.s.AdminToken); w.Code != http.StatusOK {
		t.Fatalf("200-rune q: %d %s", w.Code, w.Body)
	}
}

func TestListAnalysisStatusAndSignals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	repeated := *f.send(alertEvent("a-1", "fp-a", "critical", now)).IncidentID
	f.send(alertEvent("a-2", "fp-a", "critical", now.Add(time.Minute)))
	f.send(alertEvent("a-3", "fp-a", "critical", now.Add(2*time.Minute)))
	f.sendDeploy(deployEvent("d-1", now.Add(-5*time.Minute))) // evidence, not a signal
	noJob := *f.send(alertEvent("b-1", "fp-b", "warning", now.Add(-time.Hour))).IncidentID
	failed := *f.send(alertEvent("c-1", "fp-c", "warning", now.Add(-2*time.Hour))).IncidentID

	if _, err := f.pool.Exec(ctx, `DELETE FROM analysis_jobs WHERE incident_id = $1`, noJob); err != nil {
		t.Fatal(err)
	}
	// An older succeeded job and a newer failed one: the newest wins.
	if _, err := f.pool.Exec(ctx, `UPDATE analysis_jobs SET status = 'failed', last_error = 'boom' WHERE incident_id = $1`, failed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO analysis_jobs (incident_id, reason, status, created_at)
		VALUES ($1, 'incident_created', 'succeeded', now() - interval '1 hour')`, failed); err != nil {
		t.Fatal(err)
	}

	byID := map[string]listItem{}
	for _, it := range f.list("").Items {
		byID[it.ID] = it
	}
	want := map[string]struct {
		status  string
		signals int
	}{
		repeated: {"pending", 3},
		noJob:    {"none", 1},
		failed:   {"failed", 1},
	}
	for id, w := range want {
		got := byID[id]
		if got.AnalysisStatus != w.status || got.SignalCount != w.signals {
			t.Errorf("incident %d: analysis %q signals %d, want %q %d", got.Number, got.AnalysisStatus, got.SignalCount, w.status, w.signals)
		}
		if got.Number == 0 || got.ResolutionNote != nil {
			t.Errorf("incident %s: number %d note %v", id, got.Number, got.ResolutionNote)
		}
	}

	// The detail view agrees.
	v := testutil.Decode[struct {
		Number      int64 `json:"number"`
		SignalCount int   `json:"signal_count"`
	}](t, testutil.GetJSON(t, f.h, "/v1/incidents/"+repeated, f.s.AdminToken))
	if v.Number != byID[repeated].Number || v.SignalCount != 3 {
		t.Fatalf("detail: %+v", v)
	}

	// A resolved row carries its note.
	if w := testutil.PostJSON(t, f.h, "/v1/incidents/"+noJob+"/resolve", f.s.AdminToken, map[string]any{"note": "pool exhausted"}); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d", w.Code)
	}
	for _, it := range f.list("status=resolved").Items {
		if it.ResolutionNote == nil || *it.ResolutionNote != "pool exhausted" {
			t.Fatalf("resolved row note: %v", it.ResolutionNote)
		}
	}
}

func TestResolveRecordsUser(t *testing.T) {
	f := newFixture(t)
	manual := *f.send(alertEvent("a-1", "fp-a", "critical", now)).IncidentID
	byAlert := *f.send(alertEvent("b-1", "fp-b", "critical", now)).IncidentID

	type resolvedBy struct {
		ResolvedBy *struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"resolved_by"`
	}
	w := testutil.PostJSON(t, f.h, "/v1/incidents/"+manual+"/resolve", f.s.AdminToken, map[string]any{"note": "rolled back"})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	got := testutil.Decode[resolvedBy](t, w)
	var adminID string
	if err := f.pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE email = 'admin@uppsel.test'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if got.ResolvedBy == nil || got.ResolvedBy.ID != adminID || got.ResolvedBy.DisplayName != "Admin" {
		t.Fatalf("resolved_by after manual resolve: %+v", got.ResolvedBy)
	}

	f.send(resolvedEvent("r-1", "fp-b", now.Add(time.Minute)))
	after := testutil.Decode[resolvedBy](t, testutil.GetJSON(t, f.h, "/v1/incidents/"+byAlert, f.s.AdminToken))
	if after.ResolvedBy != nil {
		t.Fatalf("alert resolution has resolved_by %+v", after.ResolvedBy)
	}
	open := testutil.Decode[resolvedBy](t, testutil.GetJSON(t, f.h, "/v1/incidents/"+createdOpen(f), f.s.AdminToken))
	if open.ResolvedBy != nil {
		t.Fatalf("open incident has resolved_by")
	}
}

func createdOpen(f fixture) string {
	return *f.send(alertEvent("c-1", "fp-c", "critical", now)).IncidentID
}
