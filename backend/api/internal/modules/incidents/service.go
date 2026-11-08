// Package incidents owns incident grouping, evidence linking and the
// engineer-facing incident views.
package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// The evidence window around an incident: stored events for the same
// component from WindowBefore the first alert to WindowAfter the last one
// are relevant (relevant, not causal). V0 defaults; see the ADR.
const (
	WindowBefore = 30 * time.Minute
	WindowAfter  = 15 * time.Minute
)

// EvidenceWindow returns the inclusive window investigated for an incident.
func EvidenceWindow(firstSeen, lastSeen time.Time) (start, end time.Time) {
	return firstSeen.Add(-WindowBefore).UTC(), lastSeen.Add(WindowAfter).UTC()
}

// ErrNotFound means the incident does not exist or belongs to another organization.
var ErrNotFound = errors.New("incident not found")

// Component identifies an incident's primary component.
type Component struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// Window is a time range, inclusive at both ends.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Resolution records how an incident was resolved.
type Resolution struct {
	Source string  `json:"source"`
	Note   *string `json:"note"`
}

// Incident is the engineer-facing view of an incident.
type Incident struct {
	ID             string      `json:"id"`
	ProjectID      string      `json:"project_id"`
	Component      Component   `json:"component"`
	Environment    string      `json:"environment"`
	Fingerprint    string      `json:"fingerprint"`
	Status         string      `json:"status"`
	Severity       string      `json:"severity"`
	Title          string      `json:"title"`
	FirstSeenAt    time.Time   `json:"first_seen_at"`
	LastSeenAt     time.Time   `json:"last_seen_at"`
	EvidenceWindow Window      `json:"evidence_window"`
	ResolvedAt     *time.Time  `json:"resolved_at"`
	Resolution     *Resolution `json:"resolution"`
	TriggerEventID string      `json:"trigger_event_id"`
}

// Service serves incident reads.
type Service struct {
	DB database.DBTX
}

// Get returns an incident visible to orgID, or ErrNotFound.
func (s Service) Get(ctx context.Context, orgID, incidentID string) (Incident, error) {
	r, err := incidentInOrg(ctx, s.DB, orgID, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}

	start, end := EvidenceWindow(r.FirstSeenAt, r.LastSeenAt)
	inc := Incident{
		ID:             r.ID,
		ProjectID:      r.ProjectID,
		Component:      Component{ID: r.ComponentID, Slug: r.ComponentSlug, DisplayName: r.ComponentName},
		Environment:    r.Environment,
		Fingerprint:    r.Fingerprint,
		Status:         r.Status,
		Severity:       r.Severity,
		Title:          r.Title,
		FirstSeenAt:    r.FirstSeenAt.UTC(),
		LastSeenAt:     r.LastSeenAt.UTC(),
		EvidenceWindow: Window{Start: start, End: end},
		TriggerEventID: r.TriggerEventID,
	}
	if r.ResolvedAt != nil {
		t := r.ResolvedAt.UTC()
		inc.ResolvedAt = &t
	}
	if r.ResolutionSource != nil {
		inc.Resolution = &Resolution{Source: *r.ResolutionSource, Note: r.ResolutionNote}
	}
	return inc, nil
}
