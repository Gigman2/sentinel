package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// newIncident is the row created when an alert opens an incident.
type newIncident struct {
	OrganizationID, ProjectID, ComponentID, Environment, Fingerprint string
	Severity, Title, TriggerEventID                                  string
	SeenAt, UpdatedAt                                                time.Time
}

// insertOpenIncidentIfAbsent opens an incident unless one is already open
// for the same grouping key. inserted=false means one is open (or a
// concurrent transaction just opened it).
func insertOpenIncidentIfAbsent(ctx context.Context, q database.DBTX, in newIncident) (id string, inserted bool, err error) {
	err = q.QueryRow(ctx, `
		INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint,
		                       status, severity, title, first_seen_at, last_seen_at, trigger_event_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10, $11)
		ON CONFLICT (organization_id, project_id, environment, component_id, fingerprint)
		    WHERE status = 'open' DO NOTHING
		RETURNING id::text`,
		in.OrganizationID, in.ProjectID, in.ComponentID, in.Environment, in.Fingerprint,
		domain.IncidentOpen, in.Severity, in.Title, in.SeenAt, in.TriggerEventID, in.UpdatedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// groupingKey identifies the incident an alert belongs to.
type groupingKey struct {
	OrganizationID, ProjectID, Environment, ComponentID, Fingerprint string
}

// lockedIncident is the part of an open incident that attach and resolve read.
type lockedIncident struct {
	ID, Severity string
	LastSeenAt   time.Time
}

// lockOpenIncident row-locks the open incident for key (FOR NO KEY UPDATE,
// so concurrent link inserts referencing it are not blocked).
// pgx.ErrNoRows means none is open.
func lockOpenIncident(ctx context.Context, q database.DBTX, k groupingKey) (lockedIncident, error) {
	var r lockedIncident
	err := q.QueryRow(ctx, `
		SELECT id::text, severity, last_seen_at
		FROM incidents
		WHERE organization_id = $1 AND project_id = $2 AND environment = $3
		  AND component_id = $4 AND fingerprint = $5 AND status = 'open'
		FOR NO KEY UPDATE`,
		k.OrganizationID, k.ProjectID, k.Environment, k.ComponentID, k.Fingerprint).Scan(&r.ID, &r.Severity, &r.LastSeenAt)
	return r, err
}

func updateIncidentSeen(ctx context.Context, q database.DBTX, id string, lastSeenAt time.Time, severity string, updatedAt time.Time) error {
	_, err := q.Exec(ctx, `UPDATE incidents SET last_seen_at = $2, severity = $3, updated_at = $4 WHERE id = $1`,
		id, lastSeenAt, severity, updatedAt)
	return err
}

func markIncidentResolved(ctx context.Context, q database.DBTX, id string, resolvedAt time.Time, source string, note *string, updatedAt time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE incidents
		SET status = $2, resolved_at = $3, resolution_source = $4, resolution_note = $5, updated_at = $6
		WHERE id = $1`,
		id, domain.IncidentResolved, resolvedAt, source, note, updatedAt)
	return err
}

// incidentStatusInOrgForUpdate row-locks an incident visible to orgID and
// returns its status. pgx.ErrNoRows means not found.
func incidentStatusInOrgForUpdate(ctx context.Context, q database.DBTX, orgID, incidentID string) (string, error) {
	var status string
	err := q.QueryRow(ctx, `
		SELECT status FROM incidents WHERE id = $1 AND organization_id = $2
		FOR NO KEY UPDATE`, incidentID, orgID).Scan(&status)
	return status, err
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

// listCursor is the position after which a page of incidents starts.
type listCursor struct {
	LastSeenAt time.Time
	ID         string
}

// listRow is one incident in a project listing.
type listRow struct {
	ID, Status, Severity, Title, Environment string
	ComponentSlug, ComponentName             string
	FirstSeenAt, LastSeenAt                  time.Time
}

// listIncidents returns up to limit incidents in a project, newest
// last_seen_at first (ties by id descending), after the cursor if given.
// An empty status matches every status.
func listIncidents(ctx context.Context, q database.DBTX, orgID, projectID, status string, after *listCursor, limit int) ([]listRow, error) {
	var afterAt *time.Time
	var afterID *string
	if after != nil {
		afterAt, afterID = &after.LastSeenAt, &after.ID
	}
	rows, err := q.Query(ctx, `
		SELECT i.id::text, i.status, i.severity, i.title, i.environment, c.slug, c.display_name,
		       i.first_seen_at, i.last_seen_at
		FROM incidents i
		JOIN components c ON c.id = i.component_id
		WHERE i.organization_id = $1 AND i.project_id = $2
		  AND ($3 = '' OR i.status = $3)
		  AND ($4::timestamptz IS NULL OR (i.last_seen_at, i.id) < ($4, $5::uuid))
		ORDER BY i.last_seen_at DESC, i.id DESC
		LIMIT $6`, orgID, projectID, status, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (listRow, error) {
		var r listRow
		err := row.Scan(&r.ID, &r.Status, &r.Severity, &r.Title, &r.Environment, &r.ComponentSlug, &r.ComponentName,
			&r.FirstSeenAt, &r.LastSeenAt)
		return r, err
	})
}
