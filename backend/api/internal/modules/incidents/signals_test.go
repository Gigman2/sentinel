package incidents_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

type signal struct {
	EventID     string    `json:"event_id"`
	Type        string    `json:"type"`
	OccurredAt  time.Time `json:"occurred_at"`
	Summary     string    `json:"summary"`
	Severity    *string   `json:"severity"`
	Environment string    `json:"environment"`
	Disposition string    `json:"disposition"`
	Component   struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
	} `json:"component"`
	Incident *struct {
		ID     string `json:"id"`
		Number int64  `json:"number"`
	} `json:"incident"`
}

type signalsPage struct {
	Items      []signal `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

func TestSignalsList(t *testing.T) {
	f := newFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	created := f.send(alertEvent("a-1", "fp-a", "critical", now))
	attached := f.send(alertEvent("a-2", "fp-a", "critical", now.Add(time.Minute)))
	resolved := f.send(resolvedEvent("r-1", "fp-a", now.Add(2*time.Minute)))
	unmatched := f.send(resolvedEvent("r-2", "fp-none", now.Add(3*time.Minute)))
	f.sendDeploy(deployEvent("d-1", now.Add(4*time.Minute))) // not a signal
	f.send(errorObserved("e-1", now.Add(5*time.Minute)))     // not a signal
	// A redelivered alert is stored once and listed once.
	if w := testutil.PostJSON(t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken,
		alertEvent("a-2", "fp-a", "critical", now.Add(time.Minute))); w.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	// Another organization's signal never shows up.
	if w := testutil.PostJSON(t, f.h, "/v1/ingest/"+other.MonitorIntegrationID+"/events", other.MonitorToken,
		alertEvent("o-1", "fp-a", "critical", now)); w.Code != http.StatusCreated {
		t.Fatalf("other ingest: %d %s", w.Code, w.Body)
	}

	base := "/v1/projects/" + f.s.ProjectID + "/signals"
	var got []signal
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatal("pagination did not end")
		}
		path := base + "?limit=3"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := testutil.GetJSON(t, f.h, path, f.s.AdminToken)
		if w.Code != http.StatusOK {
			t.Fatalf("signals: %d %s", w.Code, w.Body)
		}
		p := testutil.Decode[signalsPage](t, w)
		got = append(got, p.Items...)
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}

	wantOrder := []ingestResult{unmatched, resolved, attached, created}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d signals, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, w := range wantOrder {
		if got[i].EventID != w.EventID || got[i].Disposition != w.Disposition {
			t.Fatalf("signal %d: %s %s, want %s %s", i, got[i].EventID, got[i].Disposition, w.EventID, w.Disposition)
		}
	}

	first := got[3]
	if first.Type != "alert.triggered" || first.Summary == "" || first.Severity == nil || *first.Severity != "critical" ||
		first.Environment != "production" || first.Component.Slug != "payment-service" || first.Component.DisplayName == "" ||
		!first.OccurredAt.Equal(now) {
		t.Fatalf("signal shape: %+v", first)
	}
	for _, s := range got[1:] {
		if s.Incident == nil || s.Incident.ID != *created.IncidentID || s.Incident.Number != 1 {
			t.Fatalf("%s: incident %+v", s.Disposition, s.Incident)
		}
	}
	if got[0].Incident != nil {
		t.Fatalf("unmatched resolution has incident %+v", got[0].Incident)
	}

	errs := []struct {
		name, path, token string
		status            int
		code              string
	}{
		{"other org project", base, other.AdminToken, 404, "not_found"},
		{"unknown project", "/v1/projects/2dd80324-2e0e-421e-95f0-f848067c4124/signals", f.s.AdminToken, 404, "not_found"},
		{"limit 0", base + "?limit=0", f.s.AdminToken, 400, "invalid_limit"},
		{"limit 201", base + "?limit=201", f.s.AdminToken, 400, "invalid_limit"},
		{"bad cursor", base + "?cursor=%21", f.s.AdminToken, 400, "invalid_cursor"},
	}
	for _, e := range errs {
		w := testutil.GetJSON(t, f.h, e.path, e.token)
		if w.Code != e.status || testutil.ErrorCode(t, w) != e.code {
			t.Fatalf("%s: %d %s", e.name, w.Code, w.Body)
		}
	}
	if w := testutil.GetJSON(t, f.h, base+"?limit=200", f.s.AdminToken); w.Code != http.StatusOK {
		t.Fatalf("limit 200: %d", w.Code)
	}
}
