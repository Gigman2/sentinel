package incidents_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

type evidenceSource struct {
	IntegrationID *string `json:"integration_id"`
	Name          string  `json:"name"`
	Capability    *string `json:"capability"`
	EventCount    int     `json:"event_count"`
	Status        string  `json:"status"`
}

func (f fixture) sources(incidentID string) []evidenceSource {
	f.t.Helper()
	w := testutil.GetJSON(f.t, f.h, "/v1/incidents/"+incidentID, f.s.AdminToken)
	if w.Code != http.StatusOK {
		f.t.Fatalf("get incident: %d %s", w.Code, w.Body)
	}
	return testutil.Decode[struct {
		EvidenceSources []evidenceSource `json:"evidence_sources"`
	}](f.t, w).EvidenceSources
}

// TestCoverage: every integration in the incident's project is listed with
// the number of its events linked to this incident, then Logs and Metrics as
// not connected. Events linked to other incidents and other organizations'
// integrations do not count.
func TestCoverage(t *testing.T) {
	f := newFixture(t)
	if _, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"}); err != nil {
		t.Fatal(err)
	}
	inc := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	f.send(alertEvent("a-2", "payments-errors", "critical", now.Add(time.Minute))) // repeat: 2 monitor events
	other := *f.send(alertEvent("b-1", "other-fingerprint", "error", now)).IncidentID

	check := func(label string, got []evidenceSource, deploys int) {
		t.Helper()
		if len(got) != 4 {
			t.Fatalf("%s: %d sources: %+v", label, len(got), got)
		}
		deployStatus := "no_matching_data"
		if deploys > 0 {
			deployStatus = "retrieved"
		}
		d, m := got[0], got[1]
		if d.Name != "deploys" || d.IntegrationID == nil || *d.IntegrationID != f.s.DeployIntegrationID ||
			d.Capability == nil || *d.Capability != "change_source" || d.EventCount != deploys || d.Status != deployStatus {
			t.Errorf("%s: deploys %+v", label, d)
		}
		if m.Name != "monitor" || *m.IntegrationID != f.s.MonitorIntegrationID || *m.Capability != "incident_input" ||
			m.EventCount != 2 || m.Status != "retrieved" {
			t.Errorf("%s: monitor %+v", label, m)
		}
		for i, name := range []string{"Logs", "Metrics"} {
			s := got[2+i]
			if s.Name != name || s.Status != "not_connected" || s.IntegrationID != nil || s.Capability != nil || s.EventCount != 0 {
				t.Errorf("%s: %s %+v", label, name, s)
			}
		}
	}
	check("no deployment", f.sources(inc), 0)

	f.sendDeploy(deployEvent("d-1", now.Add(-5*time.Minute)))
	check("with deployment", f.sources(inc), 1)

	// The deployment is linked to both open incidents but the other one has
	// only its own alert from the monitor.
	o := f.sources(other)
	if o[1].EventCount != 1 || o[0].EventCount != 1 {
		t.Fatalf("other incident: %+v", o)
	}

	// Disabled with no linked events: not connected, not "no matching data".
	// Disabled with linked events: those events still count.
	f.exec(`UPDATE integrations SET enabled = false`)
	inc2 := f.openOther()
	d := f.sources(inc2)
	if d[0].Name != "deploys" || d[0].Status != "not_connected" || d[0].EventCount != 0 || d[0].IntegrationID == nil {
		t.Errorf("disabled deploys: %+v", d[0])
	}
	if s := f.sources(inc); s[0].Status != "retrieved" || s[1].Status != "retrieved" {
		t.Errorf("disabled with events: %+v", s[:2])
	}
}

// openOther inserts an open incident on checkout-api directly (ingest is
// refused once integrations are disabled) and links its trigger.
func (f fixture) openOther() string {
	f.t.Helper()
	var id string
	err := f.pool.QueryRow(context.Background(), `
		WITH ev AS (
		    INSERT INTO operational_events (organization_id, project_id, integration_id, component_id, environment,
		                                    type, source_event_id, occurred_at, received_at, disposition)
		    VALUES ($1, $2, $3, $4, 'production', 'alert.triggered', 'manual-1', now(), now(), 'incident_created')
		    RETURNING id),
		inc AS (
		    INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint, status,
		                           severity, title, first_seen_at, last_seen_at, trigger_event_id, number)
		    SELECT $1, $2, $4, 'production', 'manual', 'open', 'error', 'manual', now(), now(), ev.id, 99 FROM ev
		    RETURNING id, trigger_event_id)
		INSERT INTO incident_events (incident_id, event_id, reason, linked_at)
		SELECT id, trigger_event_id, 'trigger', now() FROM inc
		RETURNING incident_id::text`,
		f.s.OrgID, f.s.ProjectID, f.s.MonitorIntegrationID, f.s.Components["checkout-api"]).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}
