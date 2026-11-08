// Package auth issues and checks API tokens and attaches the authenticated
// user's principal (user, organization, role) to the request context.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/httpx"
)

// NewToken returns a new random token with the given prefix and its hash.
// Store only the hash; show the plaintext once.
func NewToken(prefix string) (plain, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// A weak token is worse than no token.
		panic("auth: crypto/rand failed: " + err.Error())
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, Hash(plain)
}

// Hash returns the hex SHA-256 of a token. Tokens carry 256 bits of
// randomness, so an unsalted fast hash is sufficient.
func Hash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Principal is the authenticated user and the organization they act in.
type Principal struct {
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role"`
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored by RequireUser (zero value if none).
func PrincipalFrom(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

// BearerToken extracts the token from an "Authorization: Bearer <token>"
// header. The scheme is case-insensitive.
func BearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

var errAmbiguous = errors.New("user belongs to more than one organization")

// RequireUser authenticates the user token and attaches its Principal.
// Unknown or revoked tokens get 401 unauthenticated. Any other database
// failure gets 503 database_unavailable and is logged (never the token).
// Tokens are not organization-scoped in V0, so a user with several
// memberships is rejected with 401 ambiguous_membership.
func RequireUser(pool *pgxpool.Pool, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := BearerToken(r)
			if !ok {
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "a valid bearer token is required")
				return
			}
			p, err := lookupPrincipal(r.Context(), pool, Hash(token))
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
			case errors.Is(err, pgx.ErrNoRows):
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "a valid bearer token is required")
			case errors.Is(err, errAmbiguous):
				httpx.WriteError(w, r, http.StatusUnauthorized, "ambiguous_membership", "this user belongs to more than one organization; organization-scoped tokens are not supported yet")
			default:
				log.Error("auth lookup failed", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
				httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
			}
		})
	}
}

func lookupPrincipal(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (Principal, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.user_id::text, m.organization_id::text, m.role
		FROM api_tokens t
		JOIN memberships m ON m.user_id = t.user_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL
		LIMIT 2`, tokenHash)
	if err != nil {
		return Principal{}, err
	}
	defer rows.Close()

	var found []Principal
	for rows.Next() {
		var p Principal
		if err := rows.Scan(&p.UserID, &p.OrganizationID, &p.Role); err != nil {
			return Principal{}, err
		}
		found = append(found, p)
	}
	if err := rows.Err(); err != nil {
		return Principal{}, err
	}
	switch len(found) {
	case 0:
		return Principal{}, pgx.ErrNoRows
	case 1:
		return found[0], nil
	default:
		return Principal{}, errAmbiguous
	}
}

// RequireAdmin allows only principals with the admin role (403 forbidden otherwise).
// It must run after RequireUser.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFrom(r.Context()).Role != domain.RoleAdmin {
			httpx.WriteError(w, r, http.StatusForbidden, "forbidden", "this action requires an organization admin")
			return
		}
		next.ServeHTTP(w, r)
	})
}
