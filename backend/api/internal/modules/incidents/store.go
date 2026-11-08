package incidents

import (
	"context"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// newIncident is the row created when an alert opens an incident.
type newIncident struct {
	OrganizationID, ProjectID, ComponentID, Environment, Fingerprint string
	Severity, Title, TriggerEventID                                  string
	SeenAt                                                           time.Time
}

func insertIncident(ctx context.Context, q database.DBTX, in newIncident) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
		INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint,
		                       status, severity, title, first_seen_at, last_seen_at, trigger_event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10)
		RETURNING id::text`,
		in.OrganizationID, in.ProjectID, in.ComponentID, in.Environment, in.Fingerprint,
		domain.IncidentOpen, in.Severity, in.Title, in.SeenAt, in.TriggerEventID).Scan(&id)
	return id, err
}

func insertLink(ctx context.Context, q database.DBTX, incidentID, eventID, reason string, linkedAt time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO incident_events (incident_id, event_id, reason, linked_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (incident_id, event_id) DO NOTHING`,
		incidentID, eventID, reason, linkedAt)
	return err
}

func setEventOutcome(ctx context.Context, q database.DBTX, eventID, disposition string, incidentID *string) error {
	_, err := q.Exec(ctx, `UPDATE operational_events SET disposition = $2, incident_id = $3 WHERE id = $1`,
		eventID, disposition, incidentID)
	return err
}

// incidentRow is an incident joined with its component, as stored.
type incidentRow struct {
	ID, ProjectID, Environment, Fingerprint, Status, Severity, Title, TriggerEventID string
	ComponentID, ComponentSlug, ComponentName                                        string
	FirstSeenAt, LastSeenAt                                                          time.Time
	ResolvedAt                                                                       *time.Time
	ResolutionSource, ResolutionNote                                                 *string
}

// incidentInOrg returns the incident only if it belongs to orgID.
// pgx.ErrNoRows means not found (or not visible to this organization).
func incidentInOrg(ctx context.Context, q database.DBTX, orgID, incidentID string) (incidentRow, error) {
	var r incidentRow
	err := q.QueryRow(ctx, `
		SELECT i.id::text, i.project_id::text, i.environment, i.fingerprint, i.status, i.severity, i.title,
		       i.trigger_event_id::text, c.id::text, c.slug, c.display_name,
		       i.first_seen_at, i.last_seen_at, i.resolved_at, i.resolution_source, i.resolution_note
		FROM incidents i
		JOIN components c ON c.id = i.component_id
		WHERE i.id = $1 AND i.organization_id = $2`, incidentID, orgID).Scan(
		&r.ID, &r.ProjectID, &r.Environment, &r.Fingerprint, &r.Status, &r.Severity, &r.Title,
		&r.TriggerEventID, &r.ComponentID, &r.ComponentSlug, &r.ComponentName,
		&r.FirstSeenAt, &r.LastSeenAt, &r.ResolvedAt, &r.ResolutionSource, &r.ResolutionNote)
	return r, err
}
