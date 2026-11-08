package incidents

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
)

// groupingAttempts bounds the insert-or-lock loop in ApplyAlertTriggered.
const groupingAttempts = 3

// errGroupingContention means the open incident for a key kept being
// resolved between the insert and the lock; the caller's transaction fails.
var errGroupingContention = errors.New("open incident changed concurrently on every attempt")

func keyOf(ev domain.Event) groupingKey {
	return groupingKey{
		OrganizationID: ev.OrganizationID, ProjectID: ev.ProjectID, Environment: ev.Environment,
		ComponentID: ev.ComponentID, Fingerprint: ev.Fingerprint,
	}
}

// ApplyAlertTriggered groups an accepted alert.triggered event inside the
// caller's transaction (READ COMMITTED). It opens a new incident for the
// grouping key, or attaches the alert to the one already open, and records
// the event's link and final disposition.
//
// It first takes the component's advisory lock (see linking.go for the lock
// order), and after creating or attaching links the stored evidence inside
// the incident's window.
//
// Opening relies on the uq_incidents_open_key partial unique index: the
// insert does nothing if an incident is open, and the open incident is then
// row-locked. If that incident is resolved between the two statements, the
// lock finds nothing and the pair is retried (a resolved incident means the
// alert opens a new one).
func ApplyAlertTriggered(ctx context.Context, tx pgx.Tx, ev domain.Event) (incidentID string, created bool, err error) {
	if err := lockComponentWindow(ctx, tx, ev); err != nil {
		return "", false, err
	}
	for range groupingAttempts {
		id, inserted, err := insertOpenIncidentIfAbsent(ctx, tx, newIncident{
			OrganizationID: ev.OrganizationID,
			ProjectID:      ev.ProjectID,
			ComponentID:    ev.ComponentID,
			Environment:    ev.Environment,
			Fingerprint:    ev.Fingerprint,
			Severity:       ev.Severity,
			Title:          ev.Summary,
			TriggerEventID: ev.ID,
			SeenAt:         ev.OccurredAt,
			UpdatedAt:      ev.ReceivedAt,
		})
		if err != nil {
			return "", false, fmt.Errorf("open incident: %w", err)
		}
		if inserted {
			return id, true, recordAndLinkWindow(ctx, tx, ev, id, domain.LinkTrigger, domain.DispositionIncidentCreated)
		}

		open, err := lockOpenIncident(ctx, tx, keyOf(ev))
		if errors.Is(err, pgx.ErrNoRows) {
			continue // resolved concurrently; try to open a new one
		}
		if err != nil {
			return "", false, fmt.Errorf("lock open incident: %w", err)
		}
		if err := attach(ctx, tx, open, ev); err != nil {
			return "", false, err
		}
		return open.ID, false, recordAndLinkWindow(ctx, tx, ev, open.ID, domain.LinkRepeatTrigger, domain.DispositionIncidentAttached)
	}
	return "", false, errGroupingContention
}

// attach widens an open incident for a repeat alert. last_seen_at only moves
// forward, first_seen_at never changes, and severity only rises.
func attach(ctx context.Context, tx pgx.Tx, open lockedIncident, ev domain.Event) error {
	lastSeen := open.LastSeenAt
	if ev.OccurredAt.After(lastSeen) {
		lastSeen = ev.OccurredAt
	}
	severity := open.Severity
	if domain.SeverityRank(ev.Severity) > domain.SeverityRank(severity) {
		severity = ev.Severity
	}
	if err := updateIncidentSeen(ctx, tx, open.ID, lastSeen, severity, ev.ReceivedAt); err != nil {
		return fmt.Errorf("attach to incident: %w", err)
	}
	return nil
}

// ApplyAlertResolved resolves the open incident with the event's grouping
// key, inside the caller's transaction. matched=false means none was open;
// the event is recorded as an unmatched resolution. A resolution that
// occurred before the incident's last alert still resolves it (see the ADR).
func ApplyAlertResolved(ctx context.Context, tx pgx.Tx, ev domain.Event) (incidentID string, matched bool, err error) {
	open, err := lockOpenIncident(ctx, tx, keyOf(ev))
	if errors.Is(err, pgx.ErrNoRows) {
		if err := setEventOutcome(ctx, tx, ev.ID, domain.DispositionUnmatchedResolution, nil); err != nil {
			return "", false, fmt.Errorf("record event outcome: %w", err)
		}
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lock open incident: %w", err)
	}
	if err := markIncidentResolved(ctx, tx, open.ID, ev.OccurredAt, ResolutionAlert, nil, ev.ReceivedAt); err != nil {
		return "", false, fmt.Errorf("resolve incident: %w", err)
	}
	return open.ID, true, record(ctx, tx, ev, open.ID, domain.LinkResolution, domain.DispositionIncidentResolved)
}

// recordAndLinkWindow records the alert's link and outcome, then links the
// stored evidence inside the incident's (possibly grown) window.
func recordAndLinkWindow(ctx context.Context, tx pgx.Tx, ev domain.Event, incidentID, reason, disposition string) error {
	if err := record(ctx, tx, ev, incidentID, reason, disposition); err != nil {
		return err
	}
	_, err := LinkWindow(ctx, tx, incidentID, ev.ReceivedAt)
	return err
}

// record links the event to the incident and stores the event's outcome.
func record(ctx context.Context, tx pgx.Tx, ev domain.Event, incidentID, reason, disposition string) error {
	if _, err := insertLink(ctx, tx, incidentID, ev.ID, reason, ev.ReceivedAt); err != nil {
		return fmt.Errorf("link %s: %w", reason, err)
	}
	if err := setEventOutcome(ctx, tx, ev.ID, disposition, &incidentID); err != nil {
		return fmt.Errorf("record event outcome: %w", err)
	}
	return nil
}
