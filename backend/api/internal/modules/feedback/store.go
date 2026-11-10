package feedback

import (
	"context"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// findingKindInOrg returns the finding's kind if it belongs to an incident
// in orgID (finding → revision → incident). pgx.ErrNoRows otherwise.
func findingKindInOrg(ctx context.Context, q database.DBTX, orgID, findingID string) (string, error) {
	var kind string
	err := q.QueryRow(ctx, `
		SELECT f.kind
		FROM findings f
		JOIN analysis_revisions r ON r.id = f.revision_id
		JOIN incidents i ON i.id = r.incident_id
		WHERE f.id = $1 AND i.organization_id = $2`, findingID, orgID).Scan(&kind)
	return kind, err
}

func upsertVerdict(ctx context.Context, q database.DBTX, findingID, userID, verdict string, at time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO finding_feedback (finding_id, user_id, verdict, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (finding_id, user_id) DO UPDATE SET verdict = EXCLUDED.verdict, updated_at = EXCLUDED.updated_at`,
		findingID, userID, verdict, at)
	return err
}

func deleteVerdict(ctx context.Context, q database.DBTX, findingID, userID string) error {
	_, err := q.Exec(ctx, `DELETE FROM finding_feedback WHERE finding_id = $1 AND user_id = $2`, findingID, userID)
	return err
}

// markDone records the check as done by userID unless it already is; the
// first person to mark it keeps the credit.
func markDone(ctx context.Context, q database.DBTX, findingID, userID string, at time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO next_check_status (finding_id, done_by_user_id, done_at) VALUES ($1, $2, $3)
		ON CONFLICT (finding_id) DO NOTHING`, findingID, userID, at)
	return err
}

func markNotDone(ctx context.Context, q database.DBTX, findingID string) error {
	_, err := q.Exec(ctx, `DELETE FROM next_check_status WHERE finding_id = $1`, findingID)
	return err
}

// checkStatus returns the check's done time and who did it (all nil if not done).
func checkStatus(ctx context.Context, q database.DBTX, findingID string) (doneAt *time.Time, doneBy, byName *string, err error) {
	err = q.QueryRow(ctx, `
		SELECT cs.done_at, u.id::text, u.display_name
		FROM (SELECT $1::uuid AS finding_id) x
		LEFT JOIN next_check_status cs ON cs.finding_id = x.finding_id
		LEFT JOIN users u ON u.id = cs.done_by_user_id`, findingID).Scan(&doneAt, &doneBy, &byName)
	return doneAt, doneBy, byName, err
}
