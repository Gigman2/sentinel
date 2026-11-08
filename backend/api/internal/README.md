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
