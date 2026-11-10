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
//
// The incident takes the project's next number. The counter row is only
// updated (and so row-locked) when no incident is open for the key, so an
// attach never consumes a number. The caller holds the component's advisory
// lock, which is the only path that opens incidents for this key, so the
// open check cannot go stale before the insert.
func insertOpenIncidentIfAbsent(ctx context.Context, q database.DBTX, in newIncident) (id string, inserted bool, err error) {
	err = q.QueryRow(ctx, `
		WITH counter AS (
		    UPDATE projects SET next_incident_number = next_incident_number + 1
		    WHERE id = $2 AND NOT EXISTS (
		        SELECT 1 FROM incidents
		        WHERE organization_id = $1 AND project_id = $2 AND environment = $4
		          AND component_id = $3 AND fingerprint = $5 AND status = 'open')
		    RETURNING next_incident_number - 1 AS number)
		INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint,
		                       status, severity, title, first_seen_at, last_seen_at, trigger_event_id, updated_at, number)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $9, $10, $11, counter.number FROM counter
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

// markIncidentResolved resolves an incident. resolvedBy is the user who
// resolved it by hand, or nil when an alert resolved it.
func markIncidentResolved(ctx context.Context, q database.DBTX, id string, resolvedAt time.Time, source string, note, resolvedBy *string, updatedAt time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE incidents
		SET status = $2, resolved_at = $3, resolution_source = $4, resolution_note = $5, updated_at = $6,
		    resolved_by_user_id = $7
		WHERE id = $1`,
		id, domain.IncidentResolved, resolvedAt, source, note, updatedAt, resolvedBy)
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

// insertLink links an event to an incident. inserted=false means it was already linked.
func insertLink(ctx context.Context, q database.DBTX, incidentID, eventID, reason string, linkedAt time.Time) (inserted bool, err error) {
	tag, err := q.Exec(ctx, `
		INSERT INTO incident_events (incident_id, event_id, reason, linked_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (incident_id, event_id) DO NOTHING`,
		incidentID, eventID, reason, linkedAt)
	return tag.RowsAffected() == 1, err
}

// lockComponent takes a transaction-scoped advisory lock on a key string.
func lockComponent(ctx context.Context, q database.DBTX, key string) error {
	_, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

// incidentSpan returns an incident's first and last alert times.
func incidentSpan(ctx context.Context, q database.DBTX, incidentID string) (first, last time.Time, err error) {
	err = q.QueryRow(ctx, `SELECT first_seen_at, last_seen_at FROM incidents WHERE id = $1`, incidentID).Scan(&first, &last)
	return first, last, err
}

// linkStoredEvidence links every stored deployment.completed and
// error.observed event on the incident's org, project, environment and
// component with occurred_at in [start, end] that is not linked yet.
func linkStoredEvidence(ctx context.Context, q database.DBTX, incidentID string, start, end, linkedAt time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `
		INSERT INTO incident_events (incident_id, event_id, reason, linked_at)
		SELECT i.id, e.id, $4, $5
		FROM incidents i
		JOIN operational_events e
		  ON e.organization_id = i.organization_id AND e.project_id = i.project_id
		 AND e.environment = i.environment AND e.component_id = i.component_id
		WHERE i.id = $1
		  AND e.type IN ($6, $7)
		  AND e.occurred_at BETWEEN $2 AND $3
		ON CONFLICT (incident_id, event_id) DO NOTHING`,
		incidentID, start, end, domain.LinkSameComponentTimeWindow, linkedAt,
		domain.EventDeploymentCompleted, domain.EventErrorObserved)
	return tag.RowsAffected(), err
}

// incidentsSpanning returns the IDs (ascending) of incidents of any status
// on a component whose first_seen_at <= firstSeenBy and last_seen_at >= lastSeenFrom.
func incidentsSpanning(ctx context.Context, q database.DBTX, ev domain.Event, firstSeenBy, lastSeenFrom time.Time) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT id::text FROM incidents
		WHERE organization_id = $1 AND project_id = $2 AND environment = $3 AND component_id = $4
		  AND first_seen_at <= $5 AND last_seen_at >= $6
		ORDER BY id`,
		ev.OrganizationID, ev.ProjectID, ev.Environment, ev.ComponentID, firstSeenBy, lastSeenFrom)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
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
	ResolvedByID, ResolvedByName                                                     *string
	Number                                                                           int64
	SignalCount                                                                      int
}

// incidentInOrg returns the incident only if it belongs to orgID.
// pgx.ErrNoRows means not found (or not visible to this organization).
func incidentInOrg(ctx context.Context, q database.DBTX, orgID, incidentID string) (incidentRow, error) {
	var r incidentRow
	err := q.QueryRow(ctx, `
		SELECT i.id::text, i.project_id::text, i.environment, i.fingerprint, i.status, i.severity, i.title,
		       i.trigger_event_id::text, c.id::text, c.slug, c.display_name,
		       i.first_seen_at, i.last_seen_at, i.resolved_at, i.resolution_source, i.resolution_note,
		       u.id::text, u.display_name, i.number, `+signalCountSQL+`
		FROM incidents i
		JOIN components c ON c.id = i.component_id
		LEFT JOIN users u ON u.id = i.resolved_by_user_id
		WHERE i.id = $1 AND i.organization_id = $2`, incidentID, orgID).Scan(
		&r.ID, &r.ProjectID, &r.Environment, &r.Fingerprint, &r.Status, &r.Severity, &r.Title,
		&r.TriggerEventID, &r.ComponentID, &r.ComponentSlug, &r.ComponentName,
		&r.FirstSeenAt, &r.LastSeenAt, &r.ResolvedAt, &r.ResolutionSource, &r.ResolutionNote,
		&r.ResolvedByID, &r.ResolvedByName, &r.Number, &r.SignalCount)
	return r, err
}

// cursor is the position after which a page starts: a timestamp and an ID.
type cursor struct {
	At time.Time
	ID string
}

// signalCountSQL counts the alerts grouped into incident i (its trigger and
// repeat triggers).
const signalCountSQL = `(SELECT count(*)::int FROM incident_events l
		WHERE l.incident_id = i.id AND l.reason IN ('trigger', 'repeat_trigger'))`

// listFilter narrows a project listing. Empty strings and a nil Since match everything.
type listFilter struct {
	Status, Severity, Component, Query string
	Since                              *time.Time
}

// listRow is one incident in a project listing. AnalysisStatus is the
// newest job's status, or nil when the incident has never had a job.
type listRow struct {
	ID, Status, Severity, Title, Environment string
	ComponentSlug, ComponentName             string
	FirstSeenAt, LastSeenAt                  time.Time
	Number                                   int64
	AnalysisStatus, ResolutionNote           *string
	SignalCount                              int
}

// listIncidents returns up to limit incidents in a project that match f,
// newest last_seen_at first (ties by id descending), after the cursor if
// given. Query is a case-insensitive substring of the title.
func listIncidents(ctx context.Context, q database.DBTX, orgID, projectID string, f listFilter, after *cursor, limit int) ([]listRow, error) {
	var afterAt *time.Time
	var afterID *string
	if after != nil {
		afterAt, afterID = &after.At, &after.ID
	}
	rows, err := q.Query(ctx, `
		SELECT i.id::text, i.status, i.severity, i.title, i.environment, c.slug, c.display_name,
		       i.first_seen_at, i.last_seen_at, i.number, i.resolution_note,
		       (SELECT j.status FROM analysis_jobs j WHERE j.incident_id = i.id
		        ORDER BY j.created_at DESC, j.id DESC LIMIT 1),
		       `+signalCountSQL+`
		FROM incidents i
		JOIN components c ON c.id = i.component_id
		WHERE i.organization_id = $1 AND i.project_id = $2
		  AND ($3 = '' OR i.status = $3)
		  AND ($4 = '' OR i.severity = $4)
		  AND ($5 = '' OR c.slug = $5)
		  AND ($6::timestamptz IS NULL OR i.last_seen_at >= $6)
		  AND ($7 = '' OR strpos(lower(i.title), lower($7)) > 0)
		  AND ($8::timestamptz IS NULL OR (i.last_seen_at, i.id) < ($8, $9::uuid))
		ORDER BY i.last_seen_at DESC, i.id DESC
		LIMIT $10`, orgID, projectID, f.Status, f.Severity, f.Component, f.Since, f.Query, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (listRow, error) {
		var r listRow
		err := row.Scan(&r.ID, &r.Status, &r.Severity, &r.Title, &r.Environment, &r.ComponentSlug, &r.ComponentName,
			&r.FirstSeenAt, &r.LastSeenAt, &r.Number, &r.ResolutionNote, &r.AnalysisStatus, &r.SignalCount)
		return r, err
	})
}

// openIncidentCount counts a project's open incidents, ignoring any filter.
func openIncidentCount(ctx context.Context, q database.DBTX, orgID, projectID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM incidents
		WHERE organization_id = $1 AND project_id = $2 AND status = 'open'`, orgID, projectID).Scan(&n)
	return n, err
}

// signalRow is one alert event in a project, with the incident it went to.
type signalRow struct {
	EventID, Type, Summary, Environment, Disposition string
	ComponentSlug, ComponentName                     string
	OccurredAt, ReceivedAt                           time.Time
	Severity, SourceURL, IncidentID                  *string
	IncidentNumber                                   *int64
}

// projectSignals returns up to limit alert.triggered and alert.resolved
// events in a project, newest occurred_at first (ties by id descending),
// after the cursor if given.
func projectSignals(ctx context.Context, q database.DBTX, orgID, projectID string, after *cursor, limit int) ([]signalRow, error) {
	var afterAt *time.Time
	var afterID *string
	if after != nil {
		afterAt, afterID = &after.At, &after.ID
	}
	rows, err := q.Query(ctx, `
		SELECT e.id::text, e.type, e.summary, e.environment, e.disposition, c.slug, c.display_name,
		       e.occurred_at, e.received_at, e.severity, e.source_url, i.id::text, i.number
		FROM operational_events e
		JOIN components c ON c.id = e.component_id
		LEFT JOIN incidents i ON i.id = e.incident_id
		WHERE e.organization_id = $1 AND e.project_id = $2
		  AND e.type IN ($3, $4)
		  AND ($5::timestamptz IS NULL OR (e.occurred_at, e.id) < ($5, $6::uuid))
		ORDER BY e.occurred_at DESC, e.id DESC
		LIMIT $7`, orgID, projectID, domain.EventAlertTriggered, domain.EventAlertResolved, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (signalRow, error) {
		var r signalRow
		err := row.Scan(&r.EventID, &r.Type, &r.Summary, &r.Environment, &r.Disposition, &r.ComponentSlug, &r.ComponentName,
			&r.OccurredAt, &r.ReceivedAt, &r.Severity, &r.SourceURL, &r.IncidentID, &r.IncidentNumber)
		return r, err
	})
}

// timelineRow is one event linked to an incident.
type timelineRow struct {
	EventID, Type, Summary, LinkReason string
	OccurredAt, ReceivedAt             time.Time
	Severity, SourceURL                *string
	Attributes, Details                map[string]any
}

// timelineEvents returns up to limit events linked to an incident, oldest
// occurred_at first (ties by event id), after the cursor if given.
func timelineEvents(ctx context.Context, q database.DBTX, incidentID string, after *cursor, limit int) ([]timelineRow, error) {
	var afterAt *time.Time
	var afterID *string
	if after != nil {
		afterAt, afterID = &after.At, &after.ID
	}
	rows, err := q.Query(ctx, `
		SELECT e.id::text, e.type, e.summary, l.reason, e.occurred_at, e.received_at,
		       e.severity, e.source_url, e.attributes, e.details
		FROM incident_events l
		JOIN operational_events e ON e.id = l.event_id
		WHERE l.incident_id = $1
		  AND ($2::timestamptz IS NULL OR (e.occurred_at, e.id) > ($2, $3::uuid))
		ORDER BY e.occurred_at, e.id
		LIMIT $4`, incidentID, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (timelineRow, error) {
		var r timelineRow
		err := row.Scan(&r.EventID, &r.Type, &r.Summary, &r.LinkReason, &r.OccurredAt, &r.ReceivedAt,
			&r.Severity, &r.SourceURL, &r.Attributes, &r.Details)
		return r, err
	})
}
