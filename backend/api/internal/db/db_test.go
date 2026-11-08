package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Gigman2/sentinel/backend/api/internal/db"
	"github.com/Gigman2/sentinel/backend/api/internal/testutil"
)

func TestMigrate_CreatesTenancyTables(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	for _, tbl := range []string{
		"organizations", "users", "memberships", "api_tokens", "projects",
		"components", "integrations", "integration_component_mappings",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+tbl).Scan(&exists); err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s missing", tbl)
		}
	}
}

func TestProjectSlugUniquePerOrganization(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)

	var orgA, orgB string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name) VALUES ('a', 'A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name) VALUES ('b', 'B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO projects (organization_id, slug, name) VALUES ($1, 'web', 'Web')`
	if _, err := pool.Exec(ctx, insert, orgA); err != nil {
		t.Fatalf("first project in org A: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, orgB); err != nil {
		t.Fatalf("same slug in org B should succeed: %v", err)
	}
	_, err := pool.Exec(ctx, insert, orgA)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate slug in org A: want SQLSTATE 23505, got %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	dsn := pool.Config().ConnString()
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("first re-run: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("second re-run: %v", err)
	}
}
