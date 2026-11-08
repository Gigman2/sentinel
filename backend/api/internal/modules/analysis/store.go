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

// newestJob returns the status and last error of the incident's most recent
// job (created_at DESC, id DESC). pgx.ErrNoRows means there are none.
func newestJob(ctx context.Context, q database.DBTX, incidentID string) (status string, lastError *string, err error) {
	err = q.QueryRow(ctx, `
		SELECT status, last_error FROM analysis_jobs
		WHERE incident_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, incidentID).Scan(&status, &lastError)
	return status, lastError, err
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
