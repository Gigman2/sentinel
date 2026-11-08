# Sentinel Phase 1: Core Investigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One authenticated alert becomes a durable incident with a source-linked, same-component timeline and cautious, evidence-cited findings from a Python rule worker. An engineer can view it through an API and a minimal React page, and the system survives duplicate delivery and worker restarts.

**Architecture:** A Go API (`backend/api`, `net/http` + pgx) authenticates integration webhooks and normalizes events. In one PostgreSQL transaction it stores the event, groups it into an incident, links window evidence and enqueues a durable analysis job. A Python worker (`backend/worker`) leases jobs from PostgreSQL (`FOR UPDATE SKIP LOCKED`), runs deterministic rules over the linked evidence and writes an immutable analysis revision. A React + Vite app (`frontend/`) reads the JSON API.

**Tech Stack:** Go 1.26 (`net/http` ServeMux patterns, `github.com/jackc/pgx/v5`, `github.com/pressly/goose/v3`, `golang.org/x/time/rate`, `github.com/prometheus/client_golang`), PostgreSQL 17, Python 3.12+ with uv (`psycopg[binary]>=3.2`, `pytest`), React 18.3.1 + Vite 5.4 + TypeScript 5.6 + Vitest 2.1 + Testing Library, Docker Compose.

**Spec:** `docs/scope/Sentinel_PRD.md` (sections 3–13) and `docs/scope/Sentinel_Phase_1_Core_Investigation.md`. Read both before starting any task.

## Delivery

- Each task is one branch (`feat/p1-NN-short-name`) and one pull request against `main`, using the repository PR template.
- CI must be green before review. The CI workflow already runs `backend/api`, `backend/worker` and `frontend` once their marker files exist.
- Work stops after each PR until it is reviewed and merged. The next task starts from the updated `main`.
- A PR may change files outside its task's list only to fix something its own tests expose; it says so under "Decisions made".

## Global Constraints

- Every human read/write is scoped by the authenticated principal's organization, never by an `organization_id` from a body, query, or path.
- Integration authentication derives organization and project; payloads cannot select a tenant.
- Event types: exactly `alert.triggered`, `alert.resolved`, `error.observed`, `deployment.completed`; `schema_version` must be `"1"`.
- Unique `(integration_id, source_event_id)` enforced in the database; replays return the existing event ID with HTTP 200 and repeat no side effects.
- One open incident per key `(organization_id, project_id, environment, component_id, fingerprint)`, enforced by a partial unique index.
- `deployment.completed` and `error.observed` never create an incident.
- Evidence window: `first_seen_at − 30m` through `last_seen_at + 15m`, inclusive (Go constants `incidents.WindowBefore`, `incidents.WindowAfter`). The window is shown to the engineer.
- Links carry a reason (`trigger`, `repeat_trigger`, `resolution`, `same_component_time_window`) and never imply causality.
- Never acknowledge an event before its transaction commits.
- Never log or return credentials; store only SHA-256 hashes of tokens (tokens are 32 random bytes, base64url, prefixed `sk_usr_` or `sk_int_`). Responses that contain a credential carry `Cache-Control: no-store`.
- No finding text may contain "root cause", "caused", or "confirmed". Observations and hypotheses cite ≥1 evidence ID; unknowns and next checks may cite none.
- Empty results are worded "no matching … found", never "healthy".
- All timestamps UTC (`timestamptz`), JSON as RFC 3339. The UI displays local time with UTC in a tooltip.
- Error body: `{"error":{"code":"<code>","message":"<safe text>"},"request_id":"<id>"}`. Every response carries `X-Request-ID`. `missing_field` means absent; `invalid_field` means present but invalid; both name the field.
- Ingest status codes: `201` new, `200` replay, `400` invalid, `401` bad credential, `403` disabled/capability, `413` too large, `422` unmapped, `429` rate limited.
- Migrations live in `backend/api/migrations/NNNN_name.sql` in goose format with `-- +goose Up` / `-- +goose Down` sections and no `StatementBegin` blocks (the Python test harness executes each Up section as one string). Every table has `created_at timestamptz NOT NULL DEFAULT now()`.
- Lock order, everywhere: per-component advisory lock → incident row lock → job rows. No code path takes them in another order.
- Go tests never call `t.Parallel()`.
- No LLM, broker, Kubernetes, or cloud dependency in this phase.

## Review Focus

1. **Replay with a different body.** The same `source_event_id` arrives with a changed summary or severity. Expected: the original event comes back unchanged (200, `replayed: true`) and nothing is updated. Pinned in Task 5.
2. **Concurrent alert and evidence for one component.** A deployment and an alert for the same component commit at the same moment. Expected: they are still linked. Pinned in Task 7.
3. **One evidence event, two incidents.** A deployment falls inside the windows of two open incidents on the same component with different fingerprints. Expected: it is linked to both, and both get reanalysis jobs. Pinned in Tasks 7 and 8.
4. **Poison job.** A job either errors every time or kills the worker mid-run every time. Expected: it ends `failed` after 5 attempts, the incident shows `failed`, and `reanalyze` creates a fresh job. Pinned in Tasks 8 and 10.
5. **Credential for the wrong integration.** Integration A's token is posted to integration B's URL, including B in another org. Expected: 401, and nothing is stored. Pinned in Task 5.

---

## File Structure

```
Makefile, docker-compose.yml, .env.example, README.md   (root; extended by tasks)
backend/api/                                 module github.com/Gigman2/sentinel/backend/api
  go.mod, Dockerfile
  migrations/embed.go                        //go:embed *.sql → migrations.FS
  migrations/0001_tenancy.sql                orgs, users, memberships, api_tokens, projects, components, integrations, mappings
  migrations/0002_events_incidents.sql       operational_events, incidents, incident_events
  migrations/0003_intake_rejections.sql
  migrations/0004_analysis.sql               analysis_jobs, analysis_revisions, findings, finding_evidence
  cmd/sentinel-api/main.go                   subcommands: migrate | seed | serve | healthcheck
  internal/db/db.go                          Open, Migrate (goose Provider, no package globals)
  internal/testutil/db.go                    NewDB(t): fresh migrated database per test
  internal/domain/domain.go                  shared types and string constants
  internal/httpx/httpx.go                    JSON/error writers, request IDs, body decoding, IsUUID
  internal/auth/auth.go                      tokens, user principal middleware
  internal/seed/seed.go                      idempotent tenant seeding (demo + tests)
  internal/tenancy/{store,handlers}.go       projects, components
  internal/integrations/{store,handlers}.go
  internal/ingest/{validate,service,handler,ratelimit}.go
  internal/incidents/{grouping,linking,store,handlers,timeline}.go
  internal/analysis/{jobs,store,handlers}.go
  internal/metrics/metrics.go
  internal/server/server.go                  New(Deps) http.Handler: all routes
backend/worker/
  pyproject.toml, uv.lock, Dockerfile
  sentinel_worker/{models,rules,store,main}.py
  tests/{conftest,helpers,test_rules,test_store,test_main}.py   (conftest reads ../api/migrations/*.sql)
frontend/
  package.json, package-lock.json, vite.config.ts, tsconfig.json, index.html, Dockerfile, nginx.conf
  src/{main.tsx,App.tsx,api.ts,format.ts}
  src/views/{TokenPrompt,IncidentList,IncidentDetail}.tsx
  src/components/{Findings,Timeline,AnalysisBanner,ResolveForm}.tsx
  src/**/*.test.tsx
scripts/demo.py                              stdlib-only scenario driver
docs/adr/0001-phase-1-defaults.md
```

Go and Python tests that touch the database read `SENTINEL_TEST_DATABASE_URL` (locally `postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable`; CI already sets it). Run `go` commands from `backend/api/`, `uv` from `backend/worker/`, and `npm` from `frontend/`.

---

### Task 1: Go module, schema foundation, migrations — PR `feat/p1-01-schema`

**Files:**
- Create: `backend/api/go.mod`, `backend/api/migrations/embed.go`, `backend/api/migrations/0001_tenancy.sql`, `backend/api/internal/db/db.go`, `backend/api/internal/testutil/db.go`, `backend/api/cmd/sentinel-api/main.go`
- Modify: `Makefile` (add `test-api`)
- Test: `backend/api/internal/db/db_test.go`

**Interfaces:**
- Produces: `migrations.FS embed.FS`; `db.Open(ctx, dsn string) (*pgxpool.Pool, error)`; `db.Migrate(ctx, dsn string) error` using `goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)` (no `goose.Set*` globals); `testutil.NewDB(t *testing.T) *pgxpool.Pool` (creates `sentinel_test_<16 hex>` from `SENTINEL_TEST_DATABASE_URL`, registers `DROP DATABASE … WITH (FORCE)` cleanup before migrating and logs a failed drop with `t.Logf`, then migrates; `t.Fatal` with a clear message if the variable is unset); `sentinel-api migrate` reading `DATABASE_URL`.

Schema `0001_tenancy.sql` (ids `uuid DEFAULT gen_random_uuid()`):

```sql
organizations(id, slug text UNIQUE NOT NULL, name text NOT NULL)
users(id, email text UNIQUE NOT NULL, display_name text NOT NULL)
memberships(organization_id FK, user_id FK, role text NOT NULL CHECK (role IN ('admin','member')), PRIMARY KEY(organization_id,user_id))
api_tokens(id, user_id FK, token_hash text UNIQUE NOT NULL, revoked_at timestamptz)
projects(id, organization_id FK, slug text NOT NULL, name text NOT NULL, UNIQUE(organization_id, slug))
components(id, organization_id FK, project_id FK, environment text NOT NULL, slug text NOT NULL,
           display_name text NOT NULL, description text, owner text, UNIQUE(project_id, environment, slug))
integrations(id, organization_id FK, project_id FK, name text NOT NULL,
             capability text NOT NULL CHECK (capability IN ('incident_input','change_source')),
             credential_hash text UNIQUE NOT NULL, enabled boolean NOT NULL DEFAULT true)
integration_component_mappings(id, integration_id FK ON DELETE CASCADE, external_component_id text NOT NULL,
             environment text NOT NULL, component_id FK, UNIQUE(integration_id, external_component_id, environment))
```

- [ ] **Step 1: Write the failing tests** in `internal/db/db_test.go`:
  - `TestMigrate_CreatesTenancyTables`: each of the eight tables exists (`to_regclass`).
  - `TestProjectSlugUniquePerOrganization`: the same slug `web` succeeds in two orgs; a second `web` in org A fails with SQLSTATE `23505` (assert via `errors.As(err, &pgErr)`).
  - `TestMigrateIsIdempotent`: `db.Migrate` twice on the same DSN returns nil both times.
- [ ] **Step 2:** `make db && cd backend/api && go test ./internal/db/...` → expect FAIL (packages missing).
- [ ] **Step 3:** Implement the interfaces above; `go mod init github.com/Gigman2/sentinel/backend/api` with `go 1.26`. Add `test-api: db` to the Makefile, running `cd backend/api && SENTINEL_TEST_DATABASE_URL=… go test -count=1 ./...`.
- [ ] **Step 4:** Re-run and expect PASS. Also run `go vet ./...`, and run `DATABASE_URL=… go run ./cmd/sentinel-api migrate` twice; the second run reports no pending migrations.
- [ ] **Step 5:** Commit `feat(api): Go module, tenancy schema and migrations`, push, open PR, confirm CI's Go job now runs and passes.

---

### Task 2: HTTP foundation, auth, seeding — PR `feat/p1-02-http-auth`

**Files:**
- Create: `internal/domain/domain.go`, `internal/httpx/httpx.go`, `internal/auth/auth.go`, `internal/seed/seed.go`, `internal/server/server.go`; modify `cmd/sentinel-api/main.go` (`seed`, `serve`, `healthcheck`)
- Test: `internal/httpx/httpx_test.go`, `internal/auth/auth_test.go`, `internal/seed/seed_test.go`, `internal/server/server_test.go`

**Interfaces:**
- Produces:
  - `httpx.WriteJSON(w, status int, v any)`; `httpx.WriteError(w, r, status int, code, message string)`; `httpx.RequestID(next) http.Handler` (accepts inbound `X-Request-ID` matching `^[A-Za-z0-9-]{1,64}$`, else generates); `httpx.RequestIDFrom(ctx) string`; `httpx.IsUUID(s string) bool`; `httpx.DecodeJSON(w, r, dst any, maxBytes int64) (status int, code string, ok bool)` rejects unknown fields (message names the field), trailing data of any kind (decode to EOF), and oversize bodies (`413 payload_too_large`); otherwise `400 invalid_json`.
  - `auth.NewToken(prefix string) (plain, hash string)`; `auth.Hash(plain string) string` (hex SHA-256); `type Principal struct{ UserID, OrganizationID, Role string }`; `auth.RequireUser(pool *pgxpool.Pool, log *slog.Logger) func(http.Handler) http.Handler`. The Bearer scheme is case-insensitive. No row (`pgx.ErrNoRows`) or revoked → 401 `unauthenticated`; any other DB error → 503 `database_unavailable`, logged with `request_id` and never the token. A user with several memberships is rejected with 401 `ambiguous_membership` (tokens aren't org-scoped in V0). Also `auth.PrincipalFrom(ctx) Principal`, `auth.WithPrincipal(ctx, Principal) context.Context`, and `auth.RequireAdmin(next)`, which returns 403 `forbidden`.
  - `seed.Config{OrgSlug, OrgName, AdminEmail, AdminToken, MonitorToken, DeployToken string}` (empty tokens → generated); `seed.Seed(ctx, pool, cfg) (seed.Result, error)`. It runs in one transaction, is idempotent on `OrgSlug`, and reconciles the admin role, mappings and capabilities on re-run. `seed.Result{OrgID, ProjectID, AdminToken, MonitorIntegrationID, MonitorToken, DeployIntegrationID, DeployToken string; Components map[string]string}` returns plaintext tokens only when they were generated in this call. It seeds:
    - the project `uppsel-production` ("Uppsel Production")
    - components `checkout-api` and `payment-service`, both in `production`
    - integration `monitor` (`incident_input`), with mappings `checkout-prod→checkout-api` and `payments-prod→payment-service`
    - integration `deploys` (`change_source`), with mapping `payment-service→payment-service`
  - `server.Deps{Pool; Logger *slog.Logger; Now func() time.Time; IngestRate rate.Limit; IngestBurst int}`. `server.New(d Deps) http.Handler` treats a zero `IngestRate`/`IngestBurst` as 20/40. `GET /healthz` returns 200 `{"status":"ok"}`, or 503 `database_unavailable`.
  - `sentinel-api seed` reads `SEED_ADMIN_TOKEN`, `SEED_MONITOR_TOKEN` and `SEED_DEPLOY_TOKEN`. It prints IDs, and prints tokens only when it generated them.
  - `sentinel-api serve` reads `DATABASE_URL` and `ADDR` (default `:8080`). It sets `ReadHeaderTimeout` 10s, `ReadTimeout` 30s, `WriteTimeout` 30s and `IdleTimeout` 120s, and logs JSON. On SIGINT/SIGTERM it calls `Shutdown` and waits for the drain to finish before closing the pool, on every exit path.
  - `sentinel-api healthcheck` GETs `http://127.0.0.1:8080/healthz` and exits 0 or 1.

- [ ] **Step 1: Write failing tests:**
  - `TestNewToken_PrefixAndHash`
  - `TestDecodeJSON`: unknown field names the field; `{"a":1}}` → 400; oversize → 413.
  - `TestSeed_IdempotentAndScoped`: a re-run returns empty plaintext tokens and the same IDs; a second org gets different IDs.
  - `TestRequireUser`: no header → 401 with `request_id`; revoked → 401; lower-case `bearer` works; a closed pool → 503 `database_unavailable` and the body does not contain the token.
  - `TestHealthz`
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement. Put shared constants in `domain`: event types, statuses `open`/`resolved`, link reasons, dispositions `incident_created`, `incident_attached`, `incident_resolved`, `unmatched_resolution` and `evidence`, severities `info` < `warning` < `error` < `critical`, capabilities and roles.
- [ ] **Step 4:** Run → PASS; `go vet` clean.
- [ ] **Step 5:** Commit `feat(api): HTTP foundation, user auth and tenant seeding`; PR.

---

### Task 3: First vertical cut — alert to incident — PR `feat/p1-03-alert-to-incident`

**Files:**
- Create: `migrations/0002_events_incidents.sql`, `internal/ingest/{validate,service,handler}.go`, `internal/incidents/{grouping,store,handlers}.go`; modify `internal/server/server.go`
- Test: `internal/ingest/handler_test.go`, `internal/incidents/handlers_test.go`, `internal/testutil/http.go` (shared test helpers: `PostJSON`, `GetJSON`, `Decode[T]`; no string-slicing JSON)

Schema `0002`:

```sql
operational_events(id, organization_id, project_id, integration_id FK, component_id FK, environment text,
  type text CHECK (type IN (...4 types...)), source_event_id text NOT NULL, occurred_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL, severity text, fingerprint text, summary text NOT NULL DEFAULT '',
  source_url text, attributes jsonb NOT NULL DEFAULT '{}', details jsonb NOT NULL DEFAULT '{}',
  disposition text NOT NULL, incident_id uuid NULL,
  CONSTRAINT uq_events_source UNIQUE (integration_id, source_event_id))
INDEX ON operational_events(organization_id, project_id, environment, component_id, occurred_at)
incidents(id, organization_id, project_id, component_id, environment, fingerprint, status text CHECK IN ('open','resolved'),
  severity text, title text, first_seen_at, last_seen_at, trigger_event_id FK operational_events,
  resolved_at timestamptz, resolution_source text CHECK IN ('alert','manual'), resolution_note text, updated_at)
CREATE UNIQUE INDEX uq_incidents_open_key ON incidents(organization_id, project_id, environment, component_id, fingerprint) WHERE status = 'open'
incident_events(incident_id FK, event_id FK, reason text NOT NULL, linked_at timestamptz NOT NULL, PRIMARY KEY(incident_id, event_id))
```

`details` holds type-specific fields (`version`, `commit_sha`, `repository`, `deployment_id`, `deployed_at`). Alert events are inserted with disposition `''` and incident `NULL`; the incidents package writes the final disposition and `incident_id` once, inside the same transaction (the `''` value is never committed).

**Interfaces:**
- Consumes: `seed.Result`, `auth.*`, `httpx.*`.
- Produces:
  - `ingest.Envelope`: JSON fields exactly as PRD §7.2, plus `version`, `deployed_at`, `commit_sha`, `repository` and `deployment_id`.
  - `ingest.Validate(e Envelope, now time.Time) *ingest.Rejection`, where `Rejection{Status int; Code, Message string}`. This task covers only the `alert.triggered` path; Task 5 completes it.
  - `incidents.ApplyAlertTriggered(ctx, tx pgx.Tx, ev domain.Event) (incidentID string, created bool, err error)`. This task only creates; Task 6 adds attach.
  - `domain.Event{ID, OrganizationID, ProjectID, IntegrationID, ComponentID, Environment, Type, SourceEventID, Severity, Fingerprint, Summary, SourceURL string; OccurredAt, ReceivedAt time.Time; Attributes, Details map[string]any}`.
  - `POST /v1/ingest/{integrationId}/events` returns `201 {"event_id","disposition","incident_id","replayed":false}`. A missing, unknown or mismatched credential, or a malformed path UUID, returns 401 `invalid_credential`. `received_at` comes from `Deps.Now`.
  - `GET /v1/incidents/{incidentId}` (behind `RequireUser`) returns `{"id","project_id","component":{"id","slug","display_name"},"environment","fingerprint","status","severity","title","first_seen_at","last_seen_at","evidence_window":{"start","end"},"resolved_at","resolution":null|{"source","note"},"trigger_event_id"}`. `evidence_window` is computed from the window constants (Task 7 defines them; this task defines `WindowBefore`/`WindowAfter` in `incidents`). Another org's incident, an unknown ID or a malformed ID returns 404 `not_found`.

- [ ] **Step 1: Write failing tests:**
  - `TestIngestAlertCreatesIncident`: the seeded monitor token posts the PRD §7.2 payload with `external_component_id: "payments-prod"` and `occurred_at` equal to test `Now`.
    - The response is 201 with `disposition: "incident_created"`.
    - The GET shows `payment-service`, status `open`, and `first_seen_at == last_seen_at`.
    - `evidence_window` runs from `occurred_at−30m` to `occurred_at+15m`.
    - There is one `trigger` link.
  - `TestIncidentSurvivesRestart`: a second `server.New` with a fresh pool reads the same incident.
  - `TestGetIncident_OtherOrgIs404`.
  - `TestIngestRejections_NothingStored`: a missing token, a wrong token or a malformed integration ID each return 401 `invalid_credential` with zero event rows.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement (auth → mapping → one transaction → commit → respond). **Step 4:** Run → PASS. Also do a manual check: `serve`, send one `curl`, restart, and the GET still returns the incident.
- [ ] **Step 5:** Commit `feat(api): authenticated alert ingestion creates durable incident`; PR.

---

### Task 4: Project, component and integration management — PR `feat/p1-04-management-api`

**Files:**
- Create: `internal/tenancy/{store,handlers}.go`, `internal/integrations/{store,handlers}.go`; modify `internal/server/server.go`, `internal/ingest/handler.go`
- Test: `internal/tenancy/handlers_test.go`, `internal/integrations/handlers_test.go`

**Interfaces:**
- Every route below is behind `RequireUser`. Integration create, PATCH, rotate and add-mapping also need `RequireAdmin`. Bodies are capped at 1 MiB, and non-UUID path IDs return 404.
  - **Projects:**
    - `POST /v1/projects {"slug","name"}` returns 201. The slug must match `^[a-z0-9][a-z0-9-]{1,62}$`, otherwise 400 `invalid_slug`. A duplicate returns 409 `slug_taken`.
    - `GET /v1/projects` returns `{"items":[{id,slug,name,created_at}]}`.
    - `tenancy.ProjectInOrg(ctx, q, orgID, projectID) error` returns `tenancy.ErrNotFound`. It is the single shared helper; integrations reuse it.
  - **Components:**
    - `POST /v1/projects/{projectId}/components {"slug","display_name","environment","description?","owner?"}` returns 201. The environment must match `^[a-z0-9][a-z0-9_-]{0,31}$`. A duplicate returns 409 `component_exists`.
    - `GET` lists the project's components.
  - **Create integration:** `POST /v1/projects/{projectId}/integrations {"name","capability","mappings":[{"external_component_id","environment","component_id"}]}` returns 201 `{…,"credential":"sk_int_…"}` with `Cache-Control: no-store`. It runs in one transaction.
    - A mapping whose component is outside the project, or whose environment differs from the component's, returns 400 `invalid_mapping`, and the message names both environments.
    - A duplicate mapping returns 409 `mapping_exists`.
  - **Change an integration:**
    - `PATCH /v1/integrations/{id} {"enabled": bool}` returns 200 with the full integration, including its mappings, via `UPDATE … RETURNING`.
    - `POST /v1/integrations/{id}/rotate-credential` returns 200 with a new credential and `Cache-Control: no-store`.
    - `POST /v1/integrations/{id}/mappings {mapping}` returns 201, with the same checks as create.
  - **List integrations:** `GET /v1/projects/{projectId}/integrations` returns items with id, project_id, name, capability, enabled, created_at and mappings. They never include a credential or hash.
  - **Ingest:** after authentication, a disabled integration returns 403 `integration_disabled`.

- [ ] **Step 1: Write failing tests:**
  - `TestCreateAndListProjects_TenantScoped`
  - `TestCreateComponentValidation`
  - `TestIntegrationCredentialShownOnce`: create has `no-store` and `sk_int_`; the list contains no `sk_int_` or hash.
  - `TestMappingEnvironmentMustMatchComponent`: a `staging` mapping onto a production component returns 400 for both create and add-mapping, and nothing is persisted.
  - `TestPatchReturnsMappings`
  - `TestDisabledIntegrationCannotIngest`
  - `TestRotateCredential`: the old credential returns 401 and the new one 201.
  - `TestMemberCannotManageIntegrations`: a member token is inserted via SQL in the test.
  - `TestCrossOrgIntegrationRoutes404`: PATCH, rotate and add-mapping each return 404.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** `go test -count=1 ./...` → PASS.
- [ ] **Step 5:** Commit `feat(api): project, component and integration management`; PR.

---

### Task 5: Complete intake — PR `feat/p1-05-intake`

**Files:**
- Create: `migrations/0003_intake_rejections.sql`, `internal/ingest/ratelimit.go`; modify `internal/ingest/{validate,service,handler}.go`
- Test: `internal/ingest/validate_test.go`, `internal/ingest/handler_test.go`

`intake_rejections(id, integration_id FK, organization_id, received_at, code text NOT NULL, source_event_id text, external_component_id text, environment text)`. It stores no payload.

**Interfaces:**
- **Handler order**, fixed: authenticate (401) → disabled (403) → rate limit (429 `rate_limited`, `Retry-After: 1`) → body size and decode (413/400) → `Validate` → capability (403 `capability_not_allowed`) → mapping (422 `unmapped_component`) → transaction. A 401 writes nothing. `intake_rejections` rows are written only for 403 `capability_not_allowed` and 422 `unmapped_component`.
- **`ingest.Validate` rules:**
  - The body is at most 64 KiB, otherwise 413 `payload_too_large`.
  - `schema_version` must be `"1"`, otherwise 400 `unsupported_schema_version`.
  - The type must be one of the four, otherwise 400 `unsupported_event_type`.
  - Required fields return `missing_field` when absent: `source_event_id`, `external_component_id` and `environment`.
  - `source_event_id` is 1–200 characters counted as runes, otherwise `invalid_field`.
  - `occurred_at` must not be more than 5 minutes in the future (400 `timestamp_in_future`) or more than 30 days in the past (400 `timestamp_too_old`). For deployments, `deployed_at` may stand in for `occurred_at`; when both are present, both are range-checked.
  - Alerts require `fingerprint` (at most 200 runes, otherwise `invalid_field`) and `severity` (one of the four, otherwise `invalid_field`). `alert.triggered` also requires `summary`. `deployment.completed` requires `version`.
  - `source_url`, if present, must be `http` or `https`, otherwise `invalid_source_url`.
  - `attributes` may have at most 50 keys, otherwise `attributes_too_large`.
- **`ingest.SanitizeAttributes(in map[string]any) map[string]any`:** drops keys at any depth, including inside arrays, that match `(?i)(password|passwd|secret|token|authorization|api[_-]?key|cookie|session)`.
- **Capability:** `incident_input` may send `alert.*` and `error.observed`; `change_source` may send only `deployment.completed`.
- **Mapping lookup:** by `(integration_id, external_component_id, environment)`. A miss returns 422 with the message `"no mapping for external_component_id 'X' in environment 'Y'; add one via POST /v1/integrations/{id}/mappings"`.
- **Replay:**
  - The event is inserted with `INSERT … ON CONFLICT ON CONSTRAINT uq_events_source DO NOTHING RETURNING id`.
  - If no row comes back, roll back and return 200 `{"event_id","disposition","incident_id","replayed":true}` from the stored row. This happens before any grouping, linking or enqueue.
  - Replays are checked before Validate's time-range check: a stored `(integration, source_event_id)` returns its original response even if the timestamp has since aged past 30 days.
- **Stored dispositions:** `error.observed` and `deployment.completed` are stored with disposition `evidence` and a null `incident_id`. `alert.resolved` is stored as `unmatched_resolution`; Task 6 adds matching.
- **Rate limiter:** `ingest.NewLimiter(r rate.Limit, burst int) *Limiter` and `(*Limiter).Allow(integrationID string) bool`, kept per integration.
- **Logging:** one `slog` line per request with `request_id`, `integration_id`, `event_id` and `outcome`. It never includes the body or the Authorization header.

- [ ] **Step 1: Write failing tests:**
  - **Validation:** a table-driven `TestValidate` covering every code above, including `missing_field` vs `invalid_field` and multibyte `source_event_id`s at 200 runes; `TestSanitizeAttributes` covering nested maps and `[{"token":"x"}]`.
  - **Replays:**
    - `TestReplayReturnsOriginal`, plus a replay of an evidence event (null `incident_id`, `evidence`).
    - `TestReplayWithDifferentBodyIgnored` (Review Focus 1).
    - `TestConcurrentReplay`: 20 goroutines give one 201 and nineteen 200s, with one row.
  - **Rejections:**
    - `TestUnmappedComponentRejected`: also checks one `intake_rejections` row.
    - `TestWrongIntegrationCredential` (Review Focus 5).
    - `TestCapabilityEnforced`
    - `TestEvidenceEventsDoNotOpenIncidents`
    - `TestRateLimited`: `IngestRate: 0.001`, burst 2, so the third request returns 429 and another integration is unaffected.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** `go test -count=1 ./...` and `go test -race ./internal/ingest/...` → PASS.
- [ ] **Step 5:** Commit `feat(api): full v0 intake with validation, dedupe and rejection records`; PR.

---

### Task 6: Incident lifecycle — PR `feat/p1-06-incident-lifecycle`

**Files:**
- Modify: `internal/incidents/{grouping,store,handlers}.go`, `internal/ingest/service.go`, `internal/server/server.go`
- Test: `internal/incidents/grouping_test.go`, `internal/incidents/handlers_test.go`

**Interfaces:**
- **`incidents.ApplyAlertTriggered`, full version:**
  - Run `INSERT … ON CONFLICT (organization_id, project_id, environment, component_id, fingerprint) WHERE status = 'open' DO NOTHING RETURNING id`.
  - If no row comes back, `SELECT … FOR NO KEY UPDATE` the open incident. Retry the insert/select pair up to 3 times when the select finds nothing, which happens if a concurrent resolve commits between the two. Stay at READ COMMITTED.
  - Attach sets `last_seen_at = GREATEST(last_seen_at, occurred_at)` and leaves `first_seen_at` unchanged. Severity rises to the higher of the two and never drops. The link reason is `repeat_trigger` and the disposition `incident_attached`.
- **`incidents.ApplyAlertResolved(ctx, tx, ev) (incidentID string, matched bool, err error)`:**
  - With an open incident on the same key: `status='resolved'`, `resolved_at = ev.OccurredAt`, `resolution_source='alert'`, link reason `resolution`, disposition `incident_resolved`.
  - Without one: disposition `unmatched_resolution`.
  - A resolve whose `occurred_at` is before the incident's `last_seen_at` still resolves it; the ADR records this (Task 12).
- **Manual resolve:** `POST /v1/incidents/{id}/resolve {"note": "..."}` is open to any member. A note over 4000 runes returns 400 `invalid_note`. An already resolved incident returns 409 `already_resolved`. Resolution source is `manual`.
- **Listing:** `GET /v1/projects/{projectId}/incidents?status=open|resolved&limit=1..100(default 50)&cursor=`.
  - Checks run in this order: project in org (404), then `invalid_status`, `invalid_limit`, `invalid_cursor`.
  - The response is `{"items":[{id,status,severity,title,component:{slug,display_name},environment,first_seen_at,last_seen_at}],"next_cursor":string|null}`, ordered by `last_seen_at DESC, id DESC`.
  - The cursor is base64url (raw) of `RFC3339Nano|uuid`.

- [ ] **Step 1: Write failing tests:**
  - **Attach:**
    - `TestRepeatAlertAttaches`: severity goes from `warning` to `critical`.
    - `TestOutOfOrderRepeatAlert`: `first_seen_at` and `last_seen_at` don't move.
    - `TestLowerSeverityRepeatDoesNotDowngrade`
  - **Grouping keys:** `TestDifferentComponentOrFingerprintSeparate`, varying one key part at a time.
  - **Concurrency:**
    - `TestConcurrentAlertsOneIncident`: 20 goroutines give one incident, 1 `incident_created`, 19 `incident_attached` and 20 links.
    - `TestTriggerRetriesWhenResolvedConcurrently`: a deterministic interleaving detected via `pg_stat_activity`.
  - **Resolution:**
    - `TestResolutionAndRecurrence`
    - `TestUnmatchedResolution`
    - `TestManualResolve`: 200, then 409; another org returns 404.
  - **Listing:** `TestListIncidentsNewestFirstAndPaginated`, including two incidents with identical `last_seen_at` across a page boundary.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** `go test -count=1 ./...`, plus `-race` on incidents and ingest → PASS.
- [ ] **Step 5:** Commit `feat(api): transactional incident grouping, resolution and listing`; PR.

---

### Task 7: Evidence window linking and timeline — PR `feat/p1-07-evidence-timeline`

**Files:**
- Create: `internal/incidents/linking.go`, `internal/incidents/timeline.go`; modify `internal/incidents/grouping.go`, `internal/ingest/service.go`, `internal/server/server.go`
- Test: `internal/incidents/linking_test.go`, `internal/incidents/timeline_test.go`

**Interfaces:**
- **Window constants:** `WindowBefore = 30 * time.Minute`, `WindowAfter = 15 * time.Minute`.
- **`lockComponentWindow(ctx, tx, ev domain.Event) error`:** runs `SELECT pg_advisory_xact_lock(hashtextextended(org||'|'||project||'|'||env||'|'||component, 0))`.
  - `ApplyAlertTriggered` takes it first, before the insert/retry loop.
  - `LinkEvidenceEvent` takes it before reading incidents.
  - A comment states the lock order.
- **`incidents.LinkWindow(ctx, tx, incidentID string, at time.Time) (added int, err error)`:**
  - Links every stored `deployment.completed`/`error.observed` event of the same org/project/environment/component whose `occurred_at` falls in `[first_seen_at − WindowBefore, last_seen_at + WindowAfter]`.
  - Reason `same_component_time_window`, `linked_at = at` (the triggering event's `ReceivedAt`), `ON CONFLICT DO NOTHING`.
  - Called after create and after attach.
- **`incidents.LinkEvidenceEvent(ctx, tx, ev) (incidentIDs []string, err error)`:**
  - Links the new evidence event to every incident, of any status, on the same org/project/environment/component whose window contains `ev.OccurredAt`.
  - Returns only newly linked IDs, sorted ascending and never nil.
  - Evidence events keep disposition `evidence` and `incident_id` NULL.
  - Replays never reach this function.
- **Timeline:** `GET /v1/incidents/{id}/timeline?limit=1..200(default 100)&cursor=`.
  - Checks run in this order: incident in org (404), then `invalid_limit`, `invalid_cursor`.
  - The response is `{"items":[{"event_id","type","occurred_at","received_at","summary","severity","source_url","link_reason","attributes","details"}],"next_cursor"}`, ordered by `occurred_at ASC, event_id ASC`.
  - `attributes` and `details` are always objects, and `severity` is null when absent.

- [ ] **Step 1: Write failing tests:**
  - **Linking:**
    - `TestTimelineReferenceIncident`: the deployment `payment-service` v1.8.4 at T−7m, an error at T−2m and the alert at T are all mapped to `payment-service`. Expect exactly 3 items in order, with the correct reasons and `source_url`s.
    - `TestLateDeploymentLinksWithoutMovingStart`
    - `TestEventsOutsideWindowNotLinked`: −31m and +16m are excluded; −30m and +15m are included.
    - `TestOtherComponentNotLinked`
    - `TestEvidenceLinksToBothIncidents` (Review Focus 3)
    - `TestAttachGrowsWindowAndLinksStoredEvents`
    - `TestLinkEvidenceEventReturnsOnlyNewIDs`: the second call returns an empty, non-nil slice.
    - `TestConcurrentAlertAndEvidenceStillLink` (Review Focus 2): tx A inserts evidence and links while tx B creates the alert. Detect B waiting on the advisory lock via `pg_stat_activity`, commit A, and assert the link exists. Verify that the test fails with the lock replaced by a no-op.
  - **Timeline:**
    - `TestTimelinePagination`
    - `TestTimelineNullFields`: decode into `map[string]json.RawMessage` and assert `attributes`/`details` are not `null`.
    - `TestTimelineOtherOrg404`
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** `go test -count=1 ./...`, plus `-race` on incidents and ingest → PASS.
- [ ] **Step 5:** Commit `feat(api): evidence window linking and ordered timeline`; PR.

---

### Task 8: Durable analysis jobs and analysis read model — PR `feat/p1-08-analysis-jobs`

**Files:**
- Create: `migrations/0004_analysis.sql`, `internal/analysis/{jobs,store,handlers}.go`; modify `internal/ingest/service.go`, `internal/incidents/handlers.go`, `internal/server/server.go`
- Test: `internal/analysis/jobs_test.go`, `internal/analysis/handlers_test.go`, `internal/ingest/enqueue_test.go`

Schema `0004`:

```sql
analysis_jobs(id, incident_id FK, reason text CHECK IN ('incident_created','repeat_trigger','late_event','manual_retry'),
  status text CHECK IN ('pending','running','succeeded','failed'), attempts int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 5, next_attempt_at timestamptz NOT NULL DEFAULT now(),
  lease_token uuid, lease_expires_at timestamptz, last_error text, updated_at timestamptz NOT NULL DEFAULT now())
CREATE UNIQUE INDEX uq_jobs_one_pending ON analysis_jobs(incident_id) WHERE status = 'pending'
INDEX ON analysis_jobs(status, next_attempt_at); INDEX ON analysis_jobs(status, lease_expires_at)
analysis_revisions(id, incident_id FK, job_id FK UNIQUE, revision_number int NOT NULL, UNIQUE(incident_id, revision_number))
findings(id, revision_id FK, ordinal int, kind text CHECK IN ('observation','hypothesis','unknown','next_check'),
  rule_id text NOT NULL, text text NOT NULL, limitations text, UNIQUE(revision_id, ordinal))
finding_evidence(finding_id FK, event_id FK operational_events, PRIMARY KEY(finding_id, event_id))
```

**Interfaces:**
- **`analysis.Enqueue(ctx, tx, incidentID, reason string) (jobID string, err error)`:**
  - Runs `INSERT … ON CONFLICT (incident_id) WHERE status='pending' DO NOTHING RETURNING id`; if nothing comes back, it selects the pending job.
  - It loops up to 3 times, because a worker may lease the pending job between the two statements.
- **Ingest enqueues in the same transaction:**
  - `incident_created` on create
  - `repeat_trigger` on attach
  - `late_event` for each ID from `LinkEvidenceEvent`, in the sorted order
  - nothing for resolutions
- **Test hook:** `var afterEnqueue func() error` is a package-level hook in `ingest`, set only by `export_test.go`. The comment says tests that set it must not run in parallel.
- **Reanalyze:** `POST /v1/incidents/{id}/reanalyze` is open to any member and returns 202 `{"job_id"}` with reason `manual_retry`. If a pending job already exists, it returns that job. Another org's incident, an unknown ID or a malformed ID returns 404.
- **Incident detail:** `GET /v1/incidents/{id}` gains `"analysis": {"status": "none|pending|running|succeeded|failed", "last_error", "latest_revision": null | {"id","revision_number","created_at","findings":[{"kind","rule_id","text","limitations","evidence_ids":[...]}]}}`.
  - `status` and `last_error` come from the newest job (`created_at DESC, id DESC`).
  - `latest_revision` is the highest `revision_number`. Findings are ordered by `ordinal`, and `evidence_ids` is never null.
  - All of it is read in one `REPEATABLE READ READ ONLY` transaction.

- [ ] **Step 1: Write failing tests:**
  - `TestIncidentCreationEnqueuesJobAtomically`: the hook returns an error, giving a response of 500 or more, zero incidents, jobs and events, and a successful retry of the same `source_event_id`.
  - **Enqueue behaviour:**
    - `TestReplayDoesNotEnqueue`
    - `TestResolutionDoesNotEnqueue`
    - `TestLateEventEnqueuesCoalesced`
  - **Reanalyze:** `TestReanalyze`, including after a `failed` job; another org returns 404.
  - **Read model:** `TestIncidentDetailShowsAnalysis`, covering `pending`/null, then ordered findings, `"evidence_ids":[]`, and `last_error` on failure. Check every setup `Exec` error.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** `go test -count=1 ./...`, plus `-race` on ingest and analysis → PASS.
- [ ] **Step 5:** Commit `feat(api): durable analysis jobs and analysis read model`; PR.

---

### Task 9: Python rule engine — PR `feat/p1-09-rules`

**Files:**
- Create: `backend/worker/pyproject.toml` (name `sentinel-worker`, `requires-python = ">=3.12"`, deps `psycopg[binary]>=3.2`; dev `pytest>=8`), `uv.lock`, `sentinel_worker/{__init__,models,rules}.py`
- Modify: `Makefile` (add `test-worker`, and make `test` run both)
- Test: `backend/worker/tests/test_rules.py`

**Interfaces:**
- **Models:** frozen dataclasses in `models.py`:
  - `EvidenceEvent(id: str, type: str, occurred_at: datetime, summary: str, severity: str | None, link_reason: str, details: dict[str, Any])`
  - `IncidentContext(incident_id, component_slug, environment, first_seen_at, last_seen_at, events: tuple[EvidenceEvent, ...])`. `__post_init__` rejects naive datetimes.
  - `Finding(kind: Literal["observation","hypothesis","unknown","next_check"], rule_id, text, evidence_ids: tuple[str, ...], limitations: str | None = None)`
- **`rules.analyze(ctx) -> list[Finding]`:**
  - It is deterministic. Order: observations, hypotheses, unknowns, next checks; within a kind, the order of the table below; within a rule, the order the events occurred.
  - Times are formatted as `HH:MM UTC`.
  - A missing version reads as "unknown".
  - The trigger summary comes from the earliest `trigger` event.
  - If there is no trigger or repeat-trigger event, raise `ValueError`.

| rule_id | kind | when | text | evidence |
|---|---|---|---|---|
| `trigger_alert` | observation | always | `"{component} alert '{summary of trigger}' first fired at {first_seen}."` | trigger + repeat_trigger events |
| `deployment_in_window` | observation | each deployment | `"{component} version {version} was deployed at {t}."` | that deployment |
| `errors_observed` | observation | ≥1 error | `"{n} error event(s) were observed for {component} between {t_first} and {t_last}."` | all errors |
| `deployment_near_onset` | hypothesis | each deployment with `occurred_at` in `[first_seen − 30m, first_seen]` | `"The {component} deployment of version {version} at {t} may be related to the alert at {first_seen}."`, limitations `"Timing alone cannot establish causation."` | deployment + earliest trigger |
| `error_before_alert` | hypothesis | earliest error < first_seen | `"Errors began at {t}, before the alert fired at {first_seen}; the problem may have started earlier than the alert suggests."`, limitations `"Error events show symptoms, not their origin."` | earliest error + earliest trigger |
| `no_deployment_evidence` | unknown | no deployments | `"No deployment information is available for {component} in the investigated window, so whether a change contributed is unknown."` | — |
| `no_error_evidence` | unknown | no errors | `"No matching error events were found for {component} in the investigated window."` | — |
| `compare_by_version` | next_check | ≥1 deployment | `"Compare affected {component} instances by deployed version ({versions de-duplicated in order, ", "-joined})."` | — |
| `review_change` | next_check | ≥1 deployment | `"Review what changed in {component} version {latest version}, starting with its external integrations."` | — |
| `check_change_history` | next_check | no deployments | `"Check deployment and configuration history for {component}; Sentinel has no change data for this window."` | — |
| `inspect_telemetry` | next_check | always | `"Inspect {component} logs and metrics from {first_seen − 30m} to {last_seen + 15m}."` | — |

- [ ] **Step 1: Write failing tests** with a `make_ctx(events)` helper, first_seen at 14:09 UTC:
  - **The reference incident:** `test_reference_incident` expects these exact rule IDs: `trigger_alert`, `deployment_in_window`, `errors_observed`, `deployment_near_onset`, `error_before_alert`, `compare_by_version`, `review_change` and `inspect_telemetry`. The hypothesis text must equal `"The payment-service deployment of version 1.8.4 at 14:02 UTC may be related to the alert at 14:09 UTC."`.
  - **Honesty and multiple candidates:**
    - `test_alert_only_is_honest`
    - `test_two_deployments_two_candidates`
    - `test_deployment_after_alert_not_hypothesis`
  - **Window edges and versions:**
    - `test_window_edges_inclusive`: deployments at exactly −30m and exactly first_seen are hypotheses.
    - `test_duplicate_versions_deduplicated`: `(1.8.4)` appears once.
  - **Input guards:**
    - `test_no_trigger_raises`
    - `test_naive_datetime_rejected`
  - **Wording and determinism:**
    - `test_citations_and_wording`: parametrized. Evidence must exist; the text contains no `root cause`, `caused`, `confirmed` or `healthy`.
    - `test_deterministic`
- [ ] **Step 2:** `cd backend/worker && uv run pytest tests/test_rules.py -q` → FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(worker): deterministic evidence-cited rule engine`; PR (CI's worker job activates).

---

### Task 10: Python worker — leasing, revisions, retries — PR `feat/p1-10-worker`

**Files:**
- Create: `backend/worker/sentinel_worker/{store,main}.py`, `tests/conftest.py`, `tests/helpers.py`
- Test: `backend/worker/tests/test_store.py`, `backend/worker/tests/test_main.py`

**Interfaces:**
- Consumes: the Task 8 schema; the Task 9 `analyze` function and models.
- **Test setup:**
  - `conftest.py`: fixture `conn` creates `sentinel_test_<hex>`, applies each `../api/migrations/*.sql` Up section in order, yields an autocommit connection and drops the database afterwards. Fixture `seeded` inserts the org, project, `payment-service`, an integration and a mapping.
  - `tests/helpers.py` (imported normally, not from conftest): `insert_event(conn, …) -> str` and `make_incident(conn, …) -> str`, which links events and inserts a `pending` job.
- **`Job`:** `@dataclass Job(id, incident_id, attempts, max_attempts, lease_token)`.
- **`store.lease_job(conn, lease_seconds=60) -> Job | None`** runs in one transaction:
  1. Sweep: `UPDATE analysis_jobs SET status='failed', last_error='lease expired after max attempts', lease_token=NULL, lease_expires_at=NULL, updated_at=now() WHERE status='running' AND lease_expires_at<now() AND attempts>=max_attempts`.
  2. Lease: `UPDATE … SET status='running', attempts=attempts+1, lease_token=gen_random_uuid(), lease_expires_at=now()+make_interval(secs=>%s) WHERE id = (SELECT id FROM analysis_jobs WHERE (status='pending' AND next_attempt_at<=now()) OR (status='running' AND lease_expires_at<now() AND attempts<max_attempts) ORDER BY next_attempt_at, created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING …`.
- **`store.load_context(conn, incident_id) -> IncidentContext`:** all linked events ordered by `occurred_at, id`.
- **`store.write_revision(conn, job, findings) -> str | None`** runs in one transaction:
  1. `SELECT … FROM incidents WHERE id=%s FOR NO KEY UPDATE`.
  2. A lease-guarded `UPDATE analysis_jobs SET status='succeeded', lease_token=NULL …`. Zero rows means roll back and return `None`.
  3. `INSERT analysis_revisions(revision_number = COALESCE(MAX)+1) ON CONFLICT (job_id) DO NOTHING`.
  4. Insert findings and evidence only if the revision row is new.
- **`store.fail_job(conn, job, error) -> None`:** takes no incident lock. All updates are lease-guarded.
  - If `attempts >= max_attempts`, mark it `failed` with the error.
  - Else, if another pending job exists for the incident, mark it `failed` with `"superseded by a newer pending analysis job"`.
  - Else requeue to `pending` with `next_attempt_at = now() + min(5s × 2^attempts, 300s)`, inside a savepoint. On `UniqueViolation`, fall back to the superseded path.
  - `last_error` is truncated to 500 characters, first line only, as `ClassName: message`.
- **`main.process_one(conn, analyze_fn=rules.analyze) -> bool`.**
- **`main.run(dsn, poll_seconds=2.0)`:** loops until SIGTERM/SIGINT, finishes the in-flight job, and reconnects after a `psycopg.OperationalError`. It logs JSON lines with `job_id`, `incident_id`, `outcome` and `attempts`, and never attributes or details. The entry point is `python -m sentinel_worker.main` with `DATABASE_URL`.

- [ ] **Step 1: Write failing tests:**
  - **Happy path and restarts:**
    - `test_process_reference_incident`
    - `test_no_job_returns_false`
    - `test_expired_lease_is_reclaimed`, simulating a worker restart.
  - **Lease and revision integrity:**
    - `test_stale_worker_cannot_write`
    - `test_write_revision_idempotent_per_job`
    - `test_second_revision_increments`
  - **Failures and retries:**
    - `test_poison_job_fails_after_max_attempts` (Review Focus 4)
    - `test_crashing_worker_job_fails_after_max_attempts` (Review Focus 4): set `attempts = max_attempts`, expire the lease, call `lease_job`; it returns `None` and the job is `failed` with the sweep message.
    - `test_retry_superseded_by_newer_pending_job`
    - `test_retry_unique_violation_falls_back_to_superseded`: an internal `_requeue(check_pending=False)` forces the violation.
    - `test_max_attempts_with_pending_job_keeps_real_error`
  - **Run loop:** `test_run_stops_on_signal`, which runs `run()` in a thread and sends a stop event after one job.
- [ ] **Step 2:** `cd backend/worker && uv run pytest -q -W error` → FAIL. **Step 3:** Implement. **Step 4:** PASS, and `make test-api` still passes.
- [ ] **Step 5:** Commit `feat(worker): durable job leasing, retries and immutable revisions`; PR.

---

### Task 11: Engineer view (React + Vite) — PR `feat/p1-11-frontend`

**Files:**
- Create: `frontend/package.json` (exact versions: `react@18.3.1`, `react-dom@18.3.1`, `vite@5.4.10`, `@vitejs/plugin-react@4.3.3`, `typescript@5.6.3`, `vitest@2.1.4`, `@testing-library/react@16.0.1`, `@testing-library/jest-dom@6.6.3`, `jsdom@25.0.1`), `package-lock.json`, `vite.config.ts` (dev proxy `/v1` and `/healthz` → `http://127.0.0.1:8080`; Vitest `environment: "jsdom"`), `tsconfig.json`, `index.html`, `src/main.tsx`, `src/App.tsx` (hash router: `#/` and `#/incidents/:id`), `src/api.ts`, `src/format.ts`, `src/views/{TokenPrompt,IncidentList,IncidentDetail}.tsx`, `src/components/{Findings,Timeline,AnalysisBanner,ResolveForm}.tsx`, `src/styles.css`
- Test: `src/format.test.ts`, `src/components/Findings.test.tsx`, `src/views/IncidentDetail.test.tsx`, `src/wording.test.ts`

**Interfaces:**
- **`src/api.ts`:**
  - `apiFetch<T>(method, path, body?) => Promise<T>` sends `Authorization: Bearer <token from sessionStorage "sentinel.token">`.
  - It throws `ApiError{status, code, message, requestId}`.
  - A 401 clears the token and the app shows `TokenPrompt`.
- **`src/format.ts`:**
  - `formatTime(iso: string | null) => {text: string, title: string}`: local text, with the UTC ISO string as the title, or `"-"` when null.
  - `safeHref(url: string | null) => string | null`: only `http:` or `https:` via `new URL`.
- **List view:**
  - Projects come from `GET /v1/projects`, with the last choice remembered in `sessionStorage`.
  - The incident table shows status, severity (`"-"` when empty), title link, component and environment, and last seen. "Load more" uses `next_cursor`.
- **Detail view header:** `<dl>` with Status (and severity), Component / environment, First seen, Last seen, **Investigated window** (`evidence_window.start` – `end`), a Trigger event link that scrolls to its timeline row, and Resolved / Resolution note when present.
- **Analysis banner:**
  - `pending`/`running` → "Analysis pending".
  - `failed` → "Analysis failed: {last_error}" plus a **Retry analysis** button.
  - `none` → "No analysis has run for this incident yet." plus **Retry analysis**.
- **Findings:**
  - Grouped under **Observed** (observation), **Possible leads** (hypothesis, each showing "Limitations: …"), **Unknowns** (unknown) and **Next checks** (next_check).
  - An empty group reads "No matching {group} found."
  - Evidence IDs are `<button>`s that scroll to the timeline row `evt-<event_id>`. A missing row shows "Event not on this page".
  - A line under the findings reads "Analysis revision N from {time}".
- **Timeline:**
  - Columns: time, type, severity, summary, link reason, source. Source links use `safeHref` with `rel="noopener noreferrer" target="_blank"`.
  - It loads every page. A timeline error appears inside the timeline section only, not over the whole page.
- **Polling and actions:**
  - While the analysis is `pending` or `running`, the detail refetches every 3 s and reloads the timeline when `revision_number` changes. Polling stops when the view unmounts or reaches a terminal state, and late responses from a previous route are ignored.
  - Retry and Resolve re-fetch the detail; Resolve also handles 409 `already_resolved`.
- **Rendering rules:** all server text renders through React text nodes, with no `dangerouslySetInnerHTML`. The source text never contains "root cause". The layout follows `prefers-color-scheme` light and dark and stays readable at 375 px wide.
- **`npm` scripts:** `dev`, `build` (`tsc -b && vite build`), `test` (`vitest run`).

- [ ] **Step 1: Write failing tests:**
  - **Formatting:** `format.test.ts` covers `safeHref` (rejects `javascript:` and `data:`, accepts https) and `formatTime` (null gives `"-"`).
  - **Findings:** `Findings.test.tsx` checks each kind lands under the right heading, limitations are shown, and empty groups read "No matching unknowns found.".
  - **Detail view:** `IncidentDetail.test.tsx` uses a mocked `fetch`.
    - The header shows Status, Component and "Investigated window".
    - The pending banner appears.
    - A failed status shows the Retry button.
    - The timeline renders `evt-` rows in order.
  - **Wording guard:** `wording.test.ts` reads every file under `src/` and asserts that none contains `dangerouslySetInnerHTML` or `root cause` (case-insensitive).
- [ ] **Step 2:** `cd frontend && npm test` → FAIL. **Step 3:** Implement. **Step 4:** `npm test && npm run build` → PASS.
- [ ] **Step 5:** Manual check against the running API (`make db`, `serve`, worker, the reference scenario via curl, `npm run dev`):
  - Open the reference incident.
  - Confirm the header and window, the four groups, evidence scrolling, timeline order and source links.
  - Click **Retry analysis**, then **Resolve**.
  - Record what you saw in the PR's "How to verify" section.
- [ ] **Step 6:** Commit `feat(frontend): React engineer view for incidents`; PR (CI's frontend job activates).

---

### Task 12: One-command stack, demo, metrics, docs — PR `feat/p1-12-stack-demo`

**Files:**
- Create: `backend/api/Dockerfile` (`golang:1.26` builder → `alpine`; entrypoint runs `migrate`, `seed`, `serve`), `backend/worker/Dockerfile` (python slim + uv, `uv sync --frozen --no-dev`), `frontend/Dockerfile` (node build → `nginx:alpine`), `frontend/nginx.conf`, `.env.example`, `backend/api/internal/metrics/metrics.go`, `scripts/demo.py`, `docs/adr/0001-phase-1-defaults.md`
- Modify: `docker-compose.yml`, `Makefile`, `README.md`, `backend/api/internal/server/server.go`, `backend/api/internal/ingest/handler.go`, `backend/api/cmd/sentinel-api/main.go`
- Test: `backend/api/internal/metrics/metrics_test.go`, plus the end-to-end demo run below

**Interfaces:**
- **`docker-compose.yml`** has these services:
  - `postgres`, already present.
  - `api` on `127.0.0.1:8080`: depends on a healthy `postgres`, reads `env_file: [{path: .env, required: false}]`, healthchecks with `sentinel-api healthcheck`, and uses `restart: unless-stopped`.
  - `worker`: depends on a healthy `api`, with `restart: unless-stopped`.
  - `frontend` on `127.0.0.1:5173`: nginx serves `dist/` and proxies `/v1` and `/healthz` to `api:8080`.
- **`frontend/nginx.conf`** sets two cache rules:
  - `Cache-Control: no-cache` on `index.html` and on any static file without a hash in its name.
  - Long caching on hashed assets.
- **`.env.example`:** dev-only tokens (`SEED_ADMIN_TOKEN=sk_usr_…`, `SEED_MONITOR_TOKEN=sk_int_…`, `SEED_DEPLOY_TOKEN=sk_int_…`) under a "local demo only" comment.
- **`Makefile`** targets:
  - `.env`: `cp -n .env.example .env`
  - `up`: depends on `.env`; runs `docker compose up -d --build --wait`
  - `reset`: `docker compose down -v`, then `up`
  - `test`: `test-api`, `test-worker` and `cd frontend && npm test`
  - `demo`: `python3 scripts/demo.py all`
- **Metrics:** `metrics.New(pool) (*metrics.Metrics, http.Handler)` uses a fresh `prometheus.NewRegistry()` per server.
  - Counter `sentinel_ingest_events_total{outcome}`, with outcomes `accepted`, `replayed` and `rejected_<code>`.
  - Gauges collected at scrape time: `sentinel_analysis_jobs{status}` (all four statuses, always) and `sentinel_analysis_oldest_pending_seconds`.
  - It checks `rows.Err()`, and a database error drops only the gauges, not the whole scrape (`promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError}`).
  - `GET /metrics` is unauthenticated and local only.
- **`scripts/demo.py`** (stdlib only):
  - Configuration: `SENTINEL_URL` (default `http://127.0.0.1:8080`) and tokens from the environment or `.env`. Project and integration IDs come from the API (`uppsel-production`, `monitor`, `deploys`).
  - Subcommands, each using a run nonce:
    - `happy`: base time now−6h. Deployment −7m, error −2m, then the alert. Passes when this run's three event IDs are on the timeline and there is a `deployment_near_onset` finding.
    - `missing-deployment`: base time now−4h, alert only. Passes when there is `no_deployment_evidence` and no hypothesis.
    - `late-deployment`: base time now−2h. The alert first, then the deployment at −7m. Passes when `revision_number >= 2` and the deployment comes first on the timeline.
    - `duplicate`: posts one alert twice and expects the same `event_id`, then waits for analysis.
    - `all`
  - `--no-wait` (with `happy` only) prints the incident ID without waiting.
  - Each scenario waits up to 30 s for `succeeded` and prints the timeline, the findings and the UI link (`http://127.0.0.1:5173/#/incidents/{id}`).
  - Network, timeout and non-JSON errors become a clean `FAIL` with exit code 1.
- **`README.md`:**
  - A mermaid architecture diagram: webhooks → Go API → PostgreSQL (events, incidents, jobs) → Python worker → PostgreSQL → React view.
  - `make up`, `make demo` and opening the UI.
  - A curl example.
  - Stopping the worker to see "Analysis pending".
  - `make test`.
- **`docs/adr/0001-phase-1-defaults.md`** records each decision below with a "revisit when" line:
  - the window sizes
  - the timestamp skew limits
  - the payload limits
  - the capability split
  - severity escalation
  - the out-of-order resolve behaviour
  - late events reanalyzing any incident status
  - pending-job coalescing
  - retries, the lease, the crash sweep and the superseded rule
  - the per-component advisory lock
  - the rate limit (20/40 per integration; per-org limiting deferred to Phase 2)
  - unauthenticated local `/metrics`
  - single-membership tokens
  - replays answered before time validation
  - React + Vite served by nginx
  - loopback-only ports

- [ ] **Step 1: Write failing test** `TestMetricsCountsIngestOutcomes`: one accepted, one replayed and one unmapped event give exact counter lines. A closed pool still returns 200 with the counters.
- [ ] **Step 2:** Run → FAIL; implement and wire; run → PASS.
- [ ] **Step 3:** Write the Dockerfiles, compose, nginx config, `.env.example`, Makefile targets and `scripts/demo.py`.
- [ ] **Step 4: End-to-end check:** `make reset && make demo` prints `PASS` for all four scenarios and exits 0.
- [ ] **Step 5: Worker outage:** `docker compose stop worker`, then `python3 scripts/demo.py happy --no-wait`. The detail shows `pending`. Then `docker compose start worker`; within 30 s the status is `succeeded` with exactly one revision.
- [ ] **Step 6: Restart and fresh clone:**
  - `docker compose restart api`: the demo incident is still readable.
  - Move `.env` aside: `make db` and `make test` still work.
  - Open the UI at :5173 in a browser. It renders the incident, and the console shows no CSP or app errors.
- [ ] **Step 7:** Write the README and the ADR; `make test` passes.
- [ ] **Step 8:** Commit `feat: one-command local stack, demo, metrics and docs`; PR.

---

## Acceptance mapping (Phase 1 §7)

| Scenario | Covered by |
|---|---|
| 1 Happy path | Task 7 `TestTimelineReferenceIncident`, Task 10 `test_process_reference_incident`, Task 12 `demo happy` |
| 2 Idempotency | Task 5 replay tests |
| 3 Repeated alert | Task 6 `TestRepeatAlertAttaches`, `TestConcurrentAlertsOneIncident` |
| 4 Tenant/component isolation | Other-org 404 tests in Tasks 3, 4, 6, 7, 8; Task 6 separate-key test; Task 4 mapping environment test |
| 5 Late event | Task 7 `TestLateDeploymentLinksWithoutMovingStart`, Task 8 late enqueue, Task 12 `demo late-deployment` |
| 6 Unknown cause | Task 9 `test_alert_only_is_honest`, Task 12 `demo missing-deployment` |
| 7 Worker failure | Task 10 lease and crash tests, Task 12 Step 5 |
| 8 Mapping failure | Task 5 `TestUnmappedComponentRejected`, Task 4 `TestMappingEnvironmentMustMatchComponent` |
| 9 Resolution | Task 6 `TestResolutionAndRecurrence` |
| PRD EVD-02 window | Task 3 `evidence_window`, Task 11 header |

## Calls made in this plan (not in the spec; review these)

- **Code layout:** the Go module is `github.com/Gigman2/sentinel/backend/api`. All server code lives in `backend/`, and the frontend is a separate React + Vite app served by nginx in Compose.
- **Human auth in V0:** a seeded bearer API token per user, with no login flow. Roles are `admin` and `member`, and admin is needed only for integration management. A token for a user with several memberships is rejected until tokens are org-scoped.
- **Ingest limits:**
  - Timestamps more than 5 minutes in the future or more than 30 days in the past are rejected.
  - Payloads are capped at 64 KiB, with at most 50 attribute keys. String lengths are counted in runes.
- **Capability split:** `incident_input` sends alerts and errors; `change_source` sends deployments.
- **Incident updates:**
  - Repeat alerts raise severity to the highest seen, and `first_seen_at` never moves.
  - An out-of-order resolve still resolves the incident.
- **Analysis jobs:**
  - A late event is any evidence event inside an incident's window, whatever the incident's status, and it triggers reanalysis.
  - Pending jobs coalesce to one per incident.
- **Concurrency:** alert grouping and evidence linking are serialized per component with a transaction-scoped advisory lock.
- **Worker retries:**
  - 5 attempts, backoff `min(5s × 2^attempts, 300s)`, and a 60 s lease.
  - Expired leases at max attempts are swept to `failed`.
  - A retry that would collide with a newer pending job is marked superseded.
- **Rate limit:** 20 req/s with a burst of 40, per integration, in memory.
- **Local exposure:** `/metrics` is unauthenticated, and every published port binds to 127.0.0.1.
