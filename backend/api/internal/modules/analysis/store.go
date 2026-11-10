package analysis

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// insertPendingJobIfAbsent creates a pending job unless the incident already
// has one. inserted=false means one is pending (or being created concurrently).
func insertPendingJobIfAbsent(ctx context.Context, q database.DBTX, incidentID, reason string) (id string, inserted bool, err error) {
	err = q.QueryRow(ctx, `
		INSERT INTO analysis_jobs (incident_id, reason, status)
		VALUES ($1, $2, 'pending')
		ON CONFLICT (incident_id) WHERE status = 'pending' DO NOTHING
		RETURNING id::text`, incidentID, reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// lockPendingJob row-locks the incident's pending job. A worker's lease
// (FOR UPDATE SKIP LOCKED) skips it until this transaction ends.
// pgx.ErrNoRows means no job is pending.
func lockPendingJob(ctx context.Context, q database.DBTX, incidentID string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
		SELECT id::text FROM analysis_jobs
		WHERE incident_id = $1 AND status = 'pending'
		FOR NO KEY UPDATE`, incidentID).Scan(&id)
	return id, err
}

// incidentExistsInOrg reports whether the incident belongs to orgID.
func incidentExistsInOrg(ctx context.Context, q database.DBTX, orgID, incidentID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM incidents WHERE id = $1 AND organization_id = $2)`,
		incidentID, orgID).Scan(&ok)
	return ok, err
}

type jobRow struct {
	Status     string
	LastError  *string
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// newestJob returns the incident's most recent job (created_at DESC, id
// DESC). pgx.ErrNoRows means there are none.
func newestJob(ctx context.Context, q database.DBTX, incidentID string) (jobRow, error) {
	var j jobRow
	err := q.QueryRow(ctx, `
		SELECT status, last_error, started_at, finished_at FROM analysis_jobs
		WHERE incident_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, incidentID).Scan(&j.Status, &j.LastError, &j.StartedAt, &j.FinishedAt)
	return j, err
}

func revisionCount(ctx context.Context, q database.DBTX, incidentID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM analysis_revisions WHERE incident_id = $1`, incidentID).Scan(&n)
	return n, err
}

type revisionRow struct {
	ID             string
	RevisionNumber int
	CreatedAt      time.Time
}

// latestRevision returns the incident's highest-numbered revision.
// pgx.ErrNoRows means there are none.
func latestRevision(ctx context.Context, q database.DBTX, incidentID string) (revisionRow, error) {
	var r revisionRow
	err := q.QueryRow(ctx, `
		SELECT id::text, revision_number, created_at FROM analysis_revisions
		WHERE incident_id = $1
		ORDER BY revision_number DESC
		LIMIT 1`, incidentID).Scan(&r.ID, &r.RevisionNumber, &r.CreatedAt)
	return r, err
}

// revisionByNumber returns one revision of the incident.
// pgx.ErrNoRows means there is no such revision.
func revisionByNumber(ctx context.Context, q database.DBTX, incidentID string, n int) (revisionRow, error) {
	var r revisionRow
	err := q.QueryRow(ctx, `
		SELECT id::text, revision_number, created_at FROM analysis_revisions
		WHERE incident_id = $1 AND revision_number = $2`, incidentID, n).Scan(&r.ID, &r.RevisionNumber, &r.CreatedAt)
	return r, err
}

type historyRow struct {
	ID             string
	RevisionNumber int
	CreatedAt      time.Time
	Reason         string
	// Cutoff is when the revision's job first started; evidence linked
	// before it is attributed to this revision or an earlier one. Jobs from
	// before started_at was recorded fall back to the revision's created_at.
	Cutoff time.Time
}

// revisionHistory returns the incident's revisions, oldest first, with the
// reason and start of the job that wrote each.
func revisionHistory(ctx context.Context, q database.DBTX, incidentID string) ([]historyRow, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id::text, r.revision_number, r.created_at, j.reason, COALESCE(j.started_at, r.created_at)
		FROM analysis_revisions r
		JOIN analysis_jobs j ON j.id = r.job_id
		WHERE r.incident_id = $1
		ORDER BY r.revision_number`, incidentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (historyRow, error) {
		var h historyRow
		err := row.Scan(&h.ID, &h.RevisionNumber, &h.CreatedAt, &h.Reason, &h.Cutoff)
		return h, err
	})
}

type findingKey struct {
	RevisionID string
	Kind       string
	RuleID     string
}

// incidentFindingKeys returns (revision, kind, rule_id) for every finding of
// the incident's revisions, in ordinal order.
func incidentFindingKeys(ctx context.Context, q database.DBTX, incidentID string) ([]findingKey, error) {
	rows, err := q.Query(ctx, `
		SELECT f.revision_id::text, f.kind, f.rule_id
		FROM findings f
		JOIN analysis_revisions r ON r.id = f.revision_id
		WHERE r.incident_id = $1
		ORDER BY r.revision_number, f.ordinal`, incidentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (findingKey, error) {
		var k findingKey
		err := row.Scan(&k.RevisionID, &k.Kind, &k.RuleID)
		return k, err
	})
}

type linkRow struct {
	EventID    string
	Type       string
	ReceivedAt time.Time
	LinkedAt   time.Time
}

// incidentLinks returns the incident's linked events in the order they were
// linked.
func incidentLinks(ctx context.Context, q database.DBTX, incidentID string) ([]linkRow, error) {
	rows, err := q.Query(ctx, `
		SELECT e.id::text, e.type, e.received_at, l.linked_at
		FROM incident_events l
		JOIN operational_events e ON e.id = l.event_id
		WHERE l.incident_id = $1
		ORDER BY l.linked_at, e.received_at, e.id`, incidentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (linkRow, error) {
		var l linkRow
		err := row.Scan(&l.EventID, &l.Type, &l.ReceivedAt, &l.LinkedAt)
		return l, err
	})
}

// revisionFindings returns a revision's findings by ordinal, each with its
// cited event IDs (oldest event first; empty, never NULL).
func revisionFindings(ctx context.Context, q database.DBTX, revisionID string) ([]Finding, error) {
	rows, err := q.Query(ctx, `
		SELECT f.kind, f.rule_id, f.text, f.limitations,
		       COALESCE(array_agg(e.id::text ORDER BY e.occurred_at, e.id) FILTER (WHERE e.id IS NOT NULL), '{}')
		FROM findings f
		LEFT JOIN finding_evidence fe ON fe.finding_id = f.id
		LEFT JOIN operational_events e ON e.id = fe.event_id
		WHERE f.revision_id = $1
		GROUP BY f.id, f.ordinal
		ORDER BY f.ordinal`, revisionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Finding, error) {
		var f Finding
		err := row.Scan(&f.Kind, &f.RuleID, &f.Text, &f.Limitations, &f.EvidenceIDs)
		return f, err
	})
}
