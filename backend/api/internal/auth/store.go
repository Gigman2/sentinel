package auth

import (
	"context"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// principalsByTokenHash returns up to limit principals (one per membership)
// for a non-revoked token. It makes no decisions; RequireUser interprets the
// result.
func principalsByTokenHash(ctx context.Context, q database.DBTX, tokenHash string, limit int) ([]Principal, error) {
	rows, err := q.Query(ctx, `
		SELECT t.user_id::text, m.organization_id::text, m.role
		FROM api_tokens t
		JOIN memberships m ON m.user_id = t.user_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL
		ORDER BY m.created_at
		LIMIT $2`, tokenHash, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var found []Principal
	for rows.Next() {
		var p Principal
		if err := rows.Scan(&p.UserID, &p.OrganizationID, &p.Role); err != nil {
			return nil, err
		}
		found = append(found, p)
	}
	return found, rows.Err()
}
