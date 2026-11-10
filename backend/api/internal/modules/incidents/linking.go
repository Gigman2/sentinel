package incidents

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
)

// Lock order, everywhere: per-component advisory lock (lockComponentWindow)
// → project counter row (insertOpenIncidentIfAbsent, only when opening an
// incident) → incident row lock (lockOpenIncident,
// incidentStatusInOrgForUpdate) → job rows. A path may skip a lock but never
// takes them in another order. The counter row serializes numbering across
// components of one project; it is held only until the opening transaction
// commits.
//
// The advisory lock serializes everything that decides which events fall in
// which incident windows for one component. Without it, an alert opening an
// incident and an evidence event for the same component could commit at the
// same moment, each unable to see the other's uncommitted row, and never be
// linked. Whichever transaction takes the lock second reads after the first
// has committed (READ COMMITTED takes a new snapshot per statement).

// lockComponentWindow takes the transaction-scoped advisory lock for the
// event's organization, project, environment and component.
func lockComponentWindow(ctx context.Context, tx pgx.Tx, ev domain.Event) error {
	key := ev.OrganizationID + "|" + ev.ProjectID + "|" + ev.Environment + "|" + ev.ComponentID
	if err := lockComponent(ctx, tx, key); err != nil {
		return fmt.Errorf("lock component window: %w", err)
	}
	return nil
}

// LinkWindow links every stored deployment.completed and error.observed
// event on the incident's component whose occurred_at falls inside the
// incident's evidence window, with reason same_component_time_window and
// linked_at = at. Existing links are left alone. The caller holds the
// component's advisory lock.
func LinkWindow(ctx context.Context, tx pgx.Tx, incidentID string, at time.Time) (added int, err error) {
	first, last, err := incidentSpan(ctx, tx, incidentID)
	if err != nil {
		return 0, fmt.Errorf("read incident window: %w", err)
	}
	start, end := EvidenceWindow(first, last)
	n, err := linkStoredEvidence(ctx, tx, incidentID, start, end, at)
	if err != nil {
		return 0, fmt.Errorf("link window evidence: %w", err)
	}
	return int(n), nil
}

// LinkEvidenceEvent links a newly stored evidence event to every incident,
// open or resolved, on the same component whose evidence window contains the
// event's occurred_at. It returns the IDs of the incidents newly linked,
// ascending, and never nil. The event's own disposition (evidence) and
// incident_id (NULL) are not changed.
func LinkEvidenceEvent(ctx context.Context, tx pgx.Tx, ev domain.Event) ([]string, error) {
	if err := lockComponentWindow(ctx, tx, ev); err != nil {
		return nil, err
	}
	// occurred_at is in [first_seen_at − WindowBefore, last_seen_at + WindowAfter]
	// exactly when first_seen_at <= occurred_at + WindowBefore and
	// last_seen_at >= occurred_at − WindowAfter.
	candidates, err := incidentsSpanning(ctx, tx, ev, ev.OccurredAt.Add(WindowBefore), ev.OccurredAt.Add(-WindowAfter))
	if err != nil {
		return nil, fmt.Errorf("find incidents for evidence: %w", err)
	}
	linked := []string{}
	for _, id := range candidates {
		inserted, err := insertLink(ctx, tx, id, ev.ID, domain.LinkSameComponentTimeWindow, ev.ReceivedAt)
		if err != nil {
			return nil, fmt.Errorf("link evidence: %w", err)
		}
		if inserted {
			linked = append(linked, id)
		}
	}
	return linked, nil
}
