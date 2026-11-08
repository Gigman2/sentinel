package incidents

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
)

// ApplyAlertTriggered turns an accepted alert.triggered event into an
// incident inside the caller's transaction: it opens the incident, links the
// event as its trigger, and records the event's final disposition.
//
// This version only creates. Attaching repeat alerts to an open incident
// (and the concurrency handling that needs) comes with the incident
// lifecycle work (plan Task 6).
func ApplyAlertTriggered(ctx context.Context, tx pgx.Tx, ev domain.Event) (incidentID string, created bool, err error) {
	incidentID, err = insertIncident(ctx, tx, newIncident{
		OrganizationID: ev.OrganizationID,
		ProjectID:      ev.ProjectID,
		ComponentID:    ev.ComponentID,
		Environment:    ev.Environment,
		Fingerprint:    ev.Fingerprint,
		Severity:       ev.Severity,
		Title:          ev.Summary,
		TriggerEventID: ev.ID,
		SeenAt:         ev.OccurredAt,
	})
	if err != nil {
		return "", false, fmt.Errorf("open incident: %w", err)
	}
	if err := insertLink(ctx, tx, incidentID, ev.ID, domain.LinkTrigger, ev.ReceivedAt); err != nil {
		return "", false, fmt.Errorf("link trigger: %w", err)
	}
	if err := setEventOutcome(ctx, tx, ev.ID, domain.DispositionIncidentCreated, &incidentID); err != nil {
		return "", false, fmt.Errorf("record event outcome: %w", err)
	}
	return incidentID, true, nil
}
