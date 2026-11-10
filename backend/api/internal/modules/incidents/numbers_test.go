package incidents_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

// number returns an incident's stored number.
func (f fixture) number(incidentID string) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(context.Background(), `SELECT number FROM incidents WHERE id = $1`, incidentID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f fixture) nextNumber(projectID string) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(context.Background(), `SELECT next_incident_number FROM projects WHERE id = $1`, projectID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestIncidentNumbersSequentialPerProject(t *testing.T) {
	f := newFixture(t)
	for i, fp := range []string{"fp-1", "fp-2", "fp-3"} {
		id := *f.send(alertEvent("a-"+fp, fp, "critical", now.Add(time.Duration(i)*time.Minute))).IncidentID
		if got := f.number(id); got != int64(i+1) {
			t.Fatalf("incident %d numbered %d", i+1, got)
		}
	}

	// Another organization's project starts again at 1.
	other, err := seed.Seed(context.Background(), f.pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	w := testutil.PostJSON(t, f.h, "/v1/ingest/"+other.MonitorIntegrationID+"/events", other.MonitorToken,
		alertEvent("o-1", "fp-1", "critical", now))
	if w.Code != http.StatusCreated {
		t.Fatalf("other ingest: %d %s", w.Code, w.Body)
	}
	id := *testutil.Decode[ingestResult](t, w).IncidentID
	if got := f.number(id); got != 1 {
		t.Fatalf("other project's first incident numbered %d", got)
	}

	// The number is part of the incident view.
	v := testutil.Decode[struct {
		Number int64 `json:"number"`
	}](t, testutil.GetJSON(t, f.h, "/v1/incidents/"+id, other.AdminToken))
	if v.Number != 1 {
		t.Fatalf("view number %d", v.Number)
	}
}

// TestIncidentNumbersConcurrent opens incidents on two components of one
// project at once. Each component has its own advisory lock, so only the
// project's counter row keeps the numbers distinct and gap-free.
func TestIncidentNumbersConcurrent(t *testing.T) {
	f := newFixture(t)
	const n = 10

	ids := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := alertEvent(fmt.Sprintf("c-%02d", i), fmt.Sprintf("fp-%02d", i), "critical", now)
			if i%2 == 1 {
				body["external_component_id"] = "checkout-prod"
			}
			<-start
			w := testutil.PostJSON(t, f.h, "/v1/ingest/"+f.s.MonitorIntegrationID+"/events", f.s.MonitorToken, body)
			if w.Code != http.StatusCreated {
				t.Errorf("request %d: %d %s", i, w.Code, w.Body)
				return
			}
			ids[i] = *testutil.Decode[ingestResult](t, w).IncidentID
		}()
	}
	close(start)
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	got := make([]int, 0, n)
	for _, id := range ids {
		got = append(got, int(f.number(id)))
	}
	sort.Ints(got)
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("numbers %v, want 1..%d", got, n)
		}
	}
	if next := f.nextNumber(f.s.ProjectID); next != n+1 {
		t.Fatalf("next_incident_number %d, want %d", next, n+1)
	}
}

func TestAttachDoesNotConsumeNumber(t *testing.T) {
	f := newFixture(t)
	first := *f.send(alertEvent("a-1", "payments-errors", "critical", now)).IncidentID
	f.send(alertEvent("a-2", "payments-errors", "critical", now.Add(time.Minute)))
	f.send(alertEvent("a-3", "payments-errors", "warning", now.Add(2*time.Minute)))
	if next := f.nextNumber(f.s.ProjectID); next != 2 {
		t.Fatalf("after two repeats next_incident_number is %d, want 2", next)
	}
	second := *f.send(alertEvent("b-1", "payments-latency", "critical", now)).IncidentID
	if f.number(first) != 1 || f.number(second) != 2 {
		t.Fatalf("numbers %d, %d", f.number(first), f.number(second))
	}
}
