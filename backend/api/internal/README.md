# internal/

Packages are grouped by role. Each group may depend only on the groups listed for it. `archtest/layers_test.go` enforces these rules on production code; test files are exempt.

```
internal/
├── platform/        Infrastructure with no Sentinel business rules.
│   ├── database/    Open, Migrate
│   ├── httpx/       JSON responses, error envelope, request IDs, strict decoding
│   └── metrics/     (Task 12)
├── domain/          Shared vocabulary: event types, statuses, link reasons, dispositions.
├── auth/            API tokens, principals, RequireUser / RequireAdmin.
├── modules/         Product features, one package each:
│   ├── tenancy/       projects, components                (Task 4)
│   ├── integrations/  integrations, credentials, mappings (Task 4)
│   ├── ingest/        webhook intake                      (Tasks 3, 5)
│   ├── incidents/     grouping, evidence linking, timeline (Tasks 3, 6, 7)
│   └── analysis/      analysis jobs, read model            (Task 8)
├── server/          Route wiring: composes modules into one http.Handler.
├── support/         Not product logic.
│   ├── seed/        Idempotent demo tenant (used by `sentinel-api seed` and tests)
│   └── testutil/    Per-test databases and test helpers
└── archtest/        Tests that enforce this layout.
```

| Group | May import |
| --- | --- |
| `platform` | nothing else in `internal/` (`database` also imports the embedded SQL in `../migrations`) |
| `domain` | nothing else in `internal/` |
| `auth` | `platform`, `domain` |
| `modules` | `platform`, `domain`, `auth`, other modules |
| `server` | `platform`, `domain`, `auth`, `modules` |
| `support` | anything above |
| `cmd/` | anything |

Two rules follow from the table:
- Production code in `modules/` and `server/` never imports `support/`.
- A module that needs another module's behaviour imports it directly, and cycles are not allowed. For example, `ingest` calls `incidents` and `analysis` inside its own transaction.

## Inside a module

Each package in `modules/` (and `auth/`) separates HTTP, rules and SQL:

| File | Contains | Must not contain |
| --- | --- | --- |
| `handlers.go` | Decoding requests, calling the service, writing JSON and errors | SQL, business rules |
| `service.go` (or named rule files such as `grouping.go`, `linking.go`) | Business rules and transaction boundaries (`Begin` / `Commit`) | SQL, HTTP |
| `store.go` (or `store_*.go`) | One small, specifically named function per query, taking a `database.DBTX` (a pool or a transaction) | Decisions, HTTP |

- Store functions are specific: `InsertOpenIncidentIfAbsent` and `LockOpenIncident`, not generic `Save` or `FindAll`. Correctness often depends on the exact SQL (`ON CONFLICT`, `FOR NO KEY UPDATE`, advisory locks, `SKIP LOCKED`), so that SQL stays visible rather than hidden behind a repository abstraction.
- Stores make no decisions. If a query returns 0, 1 or several rows, the service decides what that means.
- Tests run against real PostgreSQL. There are no mock stores.
- `archtest/layers_test.go` fails when a SQL statement appears in `auth/`, `modules/` or `server/` outside a store file. `platform/` and `support/` (for example `seed`) are exempt.
