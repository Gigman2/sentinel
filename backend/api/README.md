# Sentinel API (Go)

Module: `github.com/Gigman2/sentinel/backend/api` (Go 1.26).

```
api/
├── cmd/sentinel-api/   entry point (migrate so far; seed and serve come later)
├── internal/
│   ├── db/             Open, Migrate
│   └── testutil/       NewDB: a fresh migrated database per test
├── migrations/         SQL migrations, goose format, embedded in the binary
└── Dockerfile          (later)
```

## Tests

Tests run against a real PostgreSQL. Each test gets its own throwaway database.

```sh
make test-api            # from the repo root: starts Postgres, runs go vet + go test
```

To run them by hand:

```sh
make db
cd backend/api
SENTINEL_TEST_DATABASE_URL='postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable' go test ./...
```

Never call `t.Parallel()` in this module's tests. Packages already run in parallel, and each test isolates itself with its own database.

## Migrations

```sh
DATABASE_URL='postgres://sentinel:sentinel@localhost:5432/sentinel?sslmode=disable' go run ./cmd/sentinel-api migrate
```

Migrations live in `migrations/NNNN_name.sql` with `-- +goose Up` and `-- +goose Down` sections. Don't use `StatementBegin` blocks: the Python worker's tests apply each Up section as a single string.
