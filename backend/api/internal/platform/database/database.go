// Package database opens PostgreSQL connections and applies schema migrations.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/Gigman2/sentinel/backend/api/migrations"
)

// DBTX is what store functions accept: satisfied by *pgxpool.Pool, *pgxpool.Conn
// and pgx.Tx, so the caller (a service) decides whether a query runs inside a
// transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = (pgx.Tx)(nil)
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

// UniqueViolation reports whether err is a unique-constraint violation and,
// if so, the name of the violated constraint or index.
func UniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// ForeignKeyViolation reports whether err is a foreign-key violation.
func ForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
