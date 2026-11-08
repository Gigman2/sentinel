package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestNewToken_PrefixAndHash(t *testing.T) {
	plain, hash := auth.NewToken("sk_usr_")
	if !strings.HasPrefix(plain, "sk_usr_") || len(plain) < 47 {
		t.Fatalf("token %q: wrong prefix or too short", plain)
	}
	if auth.Hash(plain) != hash || len(hash) != 64 {
		t.Fatalf("hash mismatch: %s vs %s", auth.Hash(plain), hash)
	}
	other, _ := auth.NewToken("sk_usr_")
	if other == plain {
		t.Fatal("two tokens were identical")
	}
}

// principalEcho is a handler that reports the principal RequireUser attached.
var principalEcho = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, auth.PrincipalFrom(r.Context()))
})

func call(h http.Handler, authz string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	w := httptest.NewRecorder()
	httpx.RequestID(h).ServeHTTP(w, r)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error     struct{ Code string } `json:"error"`
		RequestID string                `json:"request_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	if body.RequestID == "" {
		t.Fatal("error body missing request_id")
	}
	return body.Error.Code
}

func TestRequireUser(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	res, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := auth.RequireUser(pool, quiet)(principalEcho)

	if w := call(h, ""); w.Code != 401 || errorCode(t, w) != "unauthenticated" {
		t.Fatalf("no header: %d %s", w.Code, w.Body)
	}
	if w := call(h, "Basic abc"); w.Code != 401 {
		t.Fatalf("non-bearer scheme: %d", w.Code)
	}
	if w := call(h, "Bearer sk_usr_unknown"); w.Code != 401 || errorCode(t, w) != "unauthenticated" {
		t.Fatalf("unknown token: %d %s", w.Code, w.Body)
	}

	for _, scheme := range []string{"Bearer ", "bearer "} {
		w := call(h, scheme+res.AdminToken)
		if w.Code != 200 {
			t.Fatalf("%q valid token: %d %s", scheme, w.Code, w.Body)
		}
		var p auth.Principal
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.OrganizationID != res.OrgID || p.Role != "admin" || p.UserID == "" {
			t.Fatalf("principal %+v, want org %s admin", p, res.OrgID)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE api_tokens SET revoked_at = now() WHERE token_hash = $1`, auth.Hash(res.AdminToken)); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "Bearer "+res.AdminToken); w.Code != 401 {
		t.Fatalf("revoked token: %d", w.Code)
	}
}

func TestRequireUser_AmbiguousMembership(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	a, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "a", OrgName: "A", AdminEmail: "same@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "b", OrgName: "B", AdminEmail: "same@example.test"}); err != nil {
		t.Fatal(err)
	}
	w := call(auth.RequireUser(pool, quiet)(principalEcho), "Bearer "+a.AdminToken)
	if w.Code != 401 || errorCode(t, w) != "ambiguous_membership" {
		t.Fatalf("user in two orgs: %d %s", w.Code, w.Body)
	}
}

func TestRequireUser_DatabaseDown(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	res, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()

	called := false
	h := auth.RequireUser(pool, quiet)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := call(h, "Bearer "+res.AdminToken)
	if w.Code != 503 || errorCode(t, w) != "database_unavailable" {
		t.Fatalf("closed pool: %d %s", w.Code, w.Body)
	}
	if called {
		t.Fatal("next handler ran despite database failure")
	}
	if strings.Contains(w.Body.String(), res.AdminToken) {
		t.Fatal("response echoed the token")
	}
}

func TestRequireAdmin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := httpx.RequestID(auth.RequireAdmin(ok))

	for role, want := range map[string]int{"admin": 204, "member": 403} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u", OrganizationID: "o", Role: role}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("role %s: got %d, want %d", role, w.Code, want)
		}
		if want == 403 && errorCode(t, w) != "forbidden" {
			t.Fatalf("member: wrong code %s", w.Body)
		}
	}
}
