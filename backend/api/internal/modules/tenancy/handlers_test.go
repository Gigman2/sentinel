package tenancy_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/support/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/support/testutil"
)

type project struct {
	ID, Slug, Name string
}

type component struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Environment string `json:"environment"`
	Owner       *string
}

func setup(t *testing.T) (http.Handler, seed.Result, seed.Result) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewDB(t)
	a, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	return server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}), a, b
}

func TestCreateAndListProjects_TenantScoped(t *testing.T) {
	h, a, b := setup(t)

	w := testutil.PostJSON(t, h, "/v1/projects", a.AdminToken, map[string]string{"slug": "web", "name": "Web"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	web := testutil.Decode[project](t, w)
	if web.ID == "" || web.Slug != "web" || web.Name != "Web" {
		t.Fatalf("created %+v", web)
	}

	// The same slug is fine in another organization, but not twice in one.
	if w := testutil.PostJSON(t, h, "/v1/projects", b.AdminToken, map[string]string{"slug": "web", "name": "Web"}); w.Code != http.StatusCreated {
		t.Fatalf("same slug in other org: %d %s", w.Code, w.Body)
	}
	if w := testutil.PostJSON(t, h, "/v1/projects", a.AdminToken, map[string]string{"slug": "web", "name": "Again"}); w.Code != http.StatusConflict || testutil.ErrorCode(t, w) != "slug_taken" {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{"Web", "w", "-web", "web_app", strings.Repeat("a", 64)} {
		if w := testutil.PostJSON(t, h, "/v1/projects", a.AdminToken, map[string]string{"slug": bad, "name": "x"}); w.Code != http.StatusBadRequest || testutil.ErrorCode(t, w) != "invalid_slug" {
			t.Fatalf("slug %q: %d %s", bad, w.Code, w.Body)
		}
	}

	list := testutil.Decode[struct{ Items []project }](t, testutil.GetJSON(t, h, "/v1/projects", a.AdminToken))
	if len(list.Items) != 2 { // uppsel-production (seeded) + web
		t.Fatalf("org A projects: %+v", list.Items)
	}
	for _, p := range list.Items {
		if p.ID == "" || p.Slug == "" {
			t.Fatalf("incomplete project %+v", p)
		}
	}

	// Org B cannot see org A's project or its components.
	if w := testutil.GetJSON(t, h, "/v1/projects/"+web.ID+"/components", b.AdminToken); w.Code != http.StatusNotFound || testutil.ErrorCode(t, w) != "not_found" {
		t.Fatalf("cross-org components: %d %s", w.Code, w.Body)
	}
	if w := testutil.GetJSON(t, h, "/v1/projects/not-a-uuid/components", a.AdminToken); w.Code != http.StatusNotFound {
		t.Fatalf("malformed project id: %d", w.Code)
	}
	if w := testutil.GetJSON(t, h, "/v1/projects", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
}

func TestCreateComponentValidation(t *testing.T) {
	h, a, b := setup(t)
	path := "/v1/projects/" + a.ProjectID + "/components"

	w := testutil.PostJSON(t, h, path, a.AdminToken, map[string]any{
		"slug": "ledger", "display_name": "Ledger", "environment": "staging", "owner": "payments-team",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	c := testutil.Decode[component](t, w)
	if c.ProjectID != a.ProjectID || c.Environment != "staging" || c.Owner == nil || *c.Owner != "payments-team" {
		t.Fatalf("created %+v", c)
	}

	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"duplicate slug+environment", map[string]any{"slug": "ledger", "display_name": "L", "environment": "staging"}, 409, "component_exists"},
		{"bad slug", map[string]any{"slug": "Bad Slug", "display_name": "L", "environment": "staging"}, 400, "invalid_slug"},
		{"bad environment", map[string]any{"slug": "x1", "display_name": "L", "environment": "Prod!"}, 400, "invalid_field"},
		{"missing display name", map[string]any{"slug": "x2", "environment": "staging"}, 400, "missing_field"},
		{"unknown field", map[string]any{"slug": "x3", "display_name": "L", "environment": "staging", "team": "x"}, 400, "invalid_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := testutil.PostJSON(t, h, path, a.AdminToken, tc.body)
			if w.Code != tc.status || testutil.ErrorCode(t, w) != tc.code {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}

	// The same slug in another environment is a different component.
	if w := testutil.PostJSON(t, h, path, a.AdminToken, map[string]any{"slug": "ledger", "display_name": "Ledger", "environment": "production"}); w.Code != http.StatusCreated {
		t.Fatalf("same slug, other environment: %d %s", w.Code, w.Body)
	}

	list := testutil.Decode[struct{ Items []component }](t, testutil.GetJSON(t, h, path, a.AdminToken))
	if len(list.Items) != 5 { // 3 seeded + ledger (staging) + ledger (production)
		t.Fatalf("components: %+v", list.Items)
	}

	if w := testutil.PostJSON(t, h, path, b.AdminToken, map[string]any{"slug": "x", "display_name": "x", "environment": "staging"}); w.Code != http.StatusNotFound {
		t.Fatalf("cross-org create: %d %s", w.Code, w.Body)
	}
}
