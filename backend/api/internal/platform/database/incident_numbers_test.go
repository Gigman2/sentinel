package database_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
	"github.com/Gigman2/sentinel/backend/api/migrations"
)

// gooseSections returns a migration file's Up and Down SQL.
func gooseSections(t *testing.T, name string) (up, down string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatalf("%s has no Down section", name)
	}
	return up, down
}

// TestIncidentNumberBackfill rolls 0005 back, stores incidents the way the
// old schema did, and reapplies it: existing incidents are numbered per
// project in first_seen_at order and each counter continues after them.
func TestIncidentNumberBackfill(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	a, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	up, down := gooseSections(t, "0005_incident_numbers.sql")
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}

	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	insert := func(s seed.Result, fp string, firstSeen time.Time) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `
			WITH ev AS (
			    INSERT INTO operational_events (organization_id, project_id, integration_id, component_id, environment,
			                                    type, source_event_id, occurred_at, received_at, disposition)
			    VALUES ($1, $2, $3, $4, 'production', 'alert.triggered', $5, $6, $6, 'incident_created')
			    RETURNING id)
			INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint, status,
			                       severity, title, first_seen_at, last_seen_at, trigger_event_id)
			SELECT $1, $2, $4, 'production', $5, 'resolved', 'error', $5, $6, $6, ev.id FROM ev
			RETURNING id::text`,
			s.OrgID, s.ProjectID, s.MonitorIntegrationID, s.Components["payment-service"], fp, firstSeen).Scan(&id)
		if err != nil {
			t.Fatalf("insert %s: %v", fp, err)
		}
		return id
	}
	a2 := insert(a, "a-second", base.Add(time.Hour))
	a1 := insert(a, "a-first", base)
	a3 := insert(a, "a-third", base.Add(2*time.Hour))
	b1 := insert(b, "b-first", base.Add(time.Hour))

	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	for id, want := range map[string]int64{a1: 1, a2: 2, a3: 3, b1: 1} {
		var got int64
		if err := pool.QueryRow(ctx, `SELECT number FROM incidents WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("incident %s numbered %d, want %d", id, got, want)
		}
	}
	for project, want := range map[string]int64{a.ProjectID: 4, b.ProjectID: 2} {
		var next int64
		if err := pool.QueryRow(ctx, `SELECT next_incident_number FROM projects WHERE id = $1`, project).Scan(&next); err != nil {
			t.Fatal(err)
		}
		if next != want {
			t.Errorf("project %s next_incident_number %d, want %d", project, next, want)
		}
	}
}
