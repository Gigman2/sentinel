package incidents_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

type timelineItem struct {
	EventID    string         `json:"event_id"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	ReceivedAt time.Time      `json:"received_at"`
	Summary    string         `json:"summary"`
	Severity   *string        `json:"severity"`
	SourceURL  *string        `json:"source_url"`
	LinkReason string         `json:"link_reason"`
	Attributes map[string]any `json:"attributes"`
	Details    map[string]any `json:"details"`
}

type timelinePage struct {
	Items      []timelineItem `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func (f fixture) timeline(incidentID, query string) timelinePage {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+incidentID+"/timeline"+query, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("timeline: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[timelinePage](f.t, w)
}

// TestTimelineReferenceIncident is the PRD reference scenario: a deployment
// of payment-service, an error, then the alert.
func TestTimelineReferenceIncident(t *testing.T) {
	f := newFixture(t)
	dep := f.sendDeploy(deployEvent("d-1", now.Add(-7*time.Minute)))
	errEv := f.send(errorObserved("e-1", now.Add(-2*time.Minute)))
	alert := alertEvent("a-1", "payments-errors", "critical", now)
	alert["source_url"] = "https://monitor.example/alerts/7209"
	al := f.send(alert)

	p := f.timeline(*al.IncidentID, "")
	if len(p.Items) != 3 || p.NextCursor != nil {
		t.Fatalf("timeline: %+v", p)
	}
	want := []struct{ id, typ, reason, url string }{
		{dep.EventID, "deployment.completed", "same_component_time_window", "https://deploys.example/payments/1.8.4"},
		{errEv.EventID, "error.observed", "same_component_time_window", "https://errors.example/issues/88"},
		{al.EventID, "alert.triggered", "trigger", "https://monitor.example/alerts/7209"},
	}
	for i, w := range want {
		got := p.Items[i]
		if got.EventID != w.id || got.Type != w.typ || got.LinkReason != w.reason || got.SourceURL == nil || *got.SourceURL != w.url {
			t.Fatalf("item %d: %+v, want %+v", i, got, w)
		}
	}
	if d := p.Items[0]; !d.OccurredAt.Equal(now.Add(-7*time.Minute)) || d.Details["version"] != "1.8.4" || d.Severity != nil {
		t.Fatalf("deployment item: %+v", d)
	}
}

func TestTimelinePagination(t *testing.T) {
	f := newFixture(t)
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	// Two errors share occurred_at; ties order by event_id.
	for _, id := range []string{"e-1", "e-2"} {
		f.send(errorObserved(id, now.Add(-3*time.Minute)))
	}
	f.sendDeploy(deployEvent("d-1", now.Add(-10*time.Minute)))
	f.send(errorObserved("e-3", now.Add(time.Minute)))

	all := f.timeline(inc, "?limit=200").Items
	if len(all) != 5 {
		t.Fatalf("items: %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		if a.OccurredAt.After(b.OccurredAt) || (a.OccurredAt.Equal(b.OccurredAt) && a.EventID >= b.EventID) {
			t.Fatalf("not ordered at %d: %+v then %+v", i, a, b)
		}
	}

	var paged []string
	query := "?limit=2"
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatal("pagination did not end")
		}
		p := f.timeline(inc, query)
		for _, it := range p.Items {
			paged = append(paged, it.EventID)
		}
		if p.NextCursor == nil {
			break
		}
		query = "?limit=2&cursor=" + *p.NextCursor
	}
	var ids []string
	for _, it := range all {
		ids = append(ids, it.EventID)
	}
	if strings.Join(paged, ",") != strings.Join(ids, ",") {
		t.Fatalf("paged %v\n  all %v", paged, ids)
	}

	base := "/v1/incidents/" + inc + "/timeline"
	for name, c := range map[string]struct{ query, code string }{
		"limit 0":     {"?limit=0", "invalid_limit"},
		"limit 201":   {"?limit=201", "invalid_limit"},
		"bad cursor":  {"?cursor=%21%21", "invalid_cursor"},
		"cursor junk": {"?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("x|y")), "invalid_cursor"},
	} {
		w := testutil.GetJSON(t, f.h, base+c.query, f.s.AdminToken)
		if w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != c.code {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestTimelineNullFields(t *testing.T) {
	f := newFixture(t)
	dep := deployEvent("d-1", now.Add(-time.Minute))
	delete(dep, "source_url")
	f.sendDeploy(dep)
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID

	w := testutil.GetJSON(t, f.h, "/v1/incidents/"+inc+"/timeline", f.s.AdminToken)
	var p struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || len(p.Items) != 2 {
		t.Fatalf("timeline: %v %s", err, w.Body)
	}
	for i, it := range p.Items {
		for _, k := range []string{"attributes", "details"} {
			if v := string(it[k]); !strings.HasPrefix(v, "{") {
				t.Errorf("item %d %s = %s, want an object", i, k, v)
			}
		}
	}
	// The deployment (first) has no severity or source_url.
	for _, k := range []string{"severity", "source_url"} {
		if v := string(p.Items[0][k]); v != "null" {
			t.Errorf("deployment %s = %s, want null", k, v)
		}
	}
}

func TestTimelineOtherOrg404(t *testing.T) {
	f := newFixture(t)
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID

	for name, c := range map[string]struct{ path, token string }{
		"other org (checked before limit)": {"/v1/incidents/" + inc + "/timeline?limit=0", other.AdminToken},
		"unknown":                          {"/v1/incidents/2dd80324-2e0e-421e-95f0-f848067c4124/timeline", f.s.AdminToken},
		"malformed":                        {"/v1/incidents/nope/timeline", f.s.AdminToken},
	} {
		w := testutil.GetJSON(t, f.h, c.path, c.token)
		if w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}
