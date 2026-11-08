// Package database opens PostgreSQL connections and applies schema migrations.
package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/Gigman2/sentinel/backend/api/migrations"
)

// Open connects a pool to dsn and verifies the connection.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations to the database at dsn.
// It uses a goose Provider rather than goose's package-level state, so it is
// safe to call from concurrent tests.
func Migrate(ctx context.Context, dsn string) error {
	_, err := MigrateWithResults(ctx, dsn)
	return err
}

// MigrateWithResults applies pending migrations and reports which ran.
func MigrateWithResults(ctx context.Context, dsn string) ([]*goose.MigrationResult, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sql db: %w", err)
	}
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return results, nil
}
