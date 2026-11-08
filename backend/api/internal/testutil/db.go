// Package testutil provides helpers for tests that need a real database.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/db"
)

// EnvVar names the admin connection string tests use to create databases,
// e.g. postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable
const EnvVar = "SENTINEL_TEST_DATABASE_URL"

// NewDB creates a fresh, fully migrated database for one test and drops it
// when the test finishes. Each call gets its own uniquely named database, so
// packages can run their tests concurrently.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	adminDSN := os.Getenv(EnvVar)
	if adminDSN == "" {
		t.Fatalf("%s is not set; start Postgres with `make db` and export %s=postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable", EnvVar, EnvVar)
	}

	name := "sentinel_test_" + randomHex(t, 8)

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	// Registered before migrating so a failed migration still drops the database.
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), adminDSN)
		if err != nil {
			t.Logf("cleanup: connect admin database: %v", err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("cleanup: drop %s: %v", name, err)
		}
	})

	dsn := withDatabase(t, adminDSN, name)
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func withDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		t.Fatalf("%s must be a URL-form connection string: %q", EnvVar, dsn)
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random name: %v", err)
	}
	return hex.EncodeToString(b)
}
