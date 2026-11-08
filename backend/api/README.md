# Sentinel API (Go)

Module: `github.com/Gigman2/sentinel/backend/api` (Go 1.26).

```
api/
├── cmd/sentinel-api/   entry point: migrate | seed | serve | healthcheck
├── internal/          grouped by role; see internal/README.md for the import rules
│   ├── platform/       database, httpx (infrastructure)
│   ├── domain/         shared constants
│   ├── auth/           tokens, user authentication, admin check
│   ├── modules/        product features (added from Task 3 on)
│   ├── server/         routes
│   ├── support/        seed (demo tenant), testutil (per-test databases)
│   └── archtest/       enforces the import rules
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

## Running locally

```sh
export DATABASE_URL='postgres://sentinel:sentinel@localhost:5432/sentinel?sslmode=disable'
go run ./cmd/sentinel-api migrate
go run ./cmd/sentinel-api seed     # prints generated tokens once; set SEED_*_TOKEN to supply your own
go run ./cmd/sentinel-api serve    # ADDR defaults to :8080; GET /healthz
```

`seed` reads `SEED_ORG_SLUG`, `SEED_ORG_NAME`, `SEED_ADMIN_EMAIL`, `SEED_ADMIN_TOKEN`, `SEED_MONITOR_TOKEN` and `SEED_DEPLOY_TOKEN`. It is safe to re-run: it reconciles the admin role, integration capabilities and mappings, and it only replaces a credential when you supply a token.
