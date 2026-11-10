# Sentinel Phase 1: Ledger engineer view (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**What this replaces:** Task 11 of `2026-11-08-phase-1-core-investigation.md`. Task 12 of that plan (stack, demo, metrics, docs) runs after these tasks, renumbered as Task 17.

**Goal:** Build the engineer view to the "Sentinel · Ledger" design, with the API, worker and schema it needs: incident numbers, list filters, analysis progress and summaries, revision history, evidence-source coverage, finding feedback, next-check tracking, and registered component dependencies.

**Architecture:**
- **Task 11** builds the React app in the Ledger visual system, using only today's API.
- **Tasks 12–16** each add one backend capability end to end (migration → Go and/or Python → API → the UI piece that shows it), so every PR can be reviewed and demonstrated on its own.

**Tech stack:**
- **Frontend:** React 18.3.1, Vite 5.4.10, TypeScript 5.6.3, Vitest 2.1.4, Testing Library, plain CSS with custom properties, and `@fontsource/geist-sans@5.3.0` / `@fontsource/geist-mono@5.3.0`.
- **Backend:** Go `net/http` + pgx, goose migrations, Python worker (psycopg 3).

**Spec:**
- **Product scope:** `docs/scope/Sentinel_PRD.md` §7.5 (UI-01..UI-05) and `docs/scope/Sentinel_Phase_1_Core_Investigation.md`.
- **Visual design:** `docs/design/sentinel-ledger.html`. Open it in a browser; the "Preview state" select shows each analysis state.

## Global Constraints

- **Wording:**
  - UI source never contains `root cause` or `dangerouslySetInnerHTML` (guarded by `src/wording.test.ts`).
  - Worker-written text never contains `root cause`, `caused`, `confirmed` or `healthy`.
  - The only text labelled "Cause" is the human-written resolution note.
- **Server text:** all of it renders as React text nodes.
- **Source links:** pass through `safeHref` (`http:`/`https:` only) with `rel="noopener noreferrer" target="_blank"`.
- **Times:** displayed in **UTC** (`14:09` / `14:09:12 UTC`, as in the design), with the full ISO timestamp in `title`.
- **Theme:**
  - Colour tokens are copied verbatim from the design's `[data-theme="light"]` and `[data-theme="dark"]` blocks.
  - The default theme follows `prefers-color-scheme`; the sidebar toggle overrides it and is remembered in `localStorage` (`sentinel.theme`).
- **Layout:**
  - Readable at 375 px with no horizontal page scroll; only the incident table may scroll inside its container.
  - The detail sidebar stacks below the main column under 900 px.
- **Accessibility:** WCAG AA contrast, visible focus, and every interactive element is a real `<button>` or `<a>`. The finding kinds are distinguished by tag text and border style, not only colour.
- **Severity:**
  - The API levels stay `info | warning | error | critical`.
  - The UI shows them with the design's 3-bar glyph: critical 3 bars on a red chip, error 3, warning 2, info 1.
  - Label text is capitalised as-is ("Critical", "Error", "Warning", "Info").
- **Incident IDs:** displayed as `INC-<number>` once Task 12 lands; before that, the first 8 characters of the UUID.
- **API errors:** surface `{code, message}` and the request ID. A 401 anywhere clears the token and shows the sign-in screen.
- **Backend rules:**
  - Every new query is organisation-scoped. An incident or finding in another organisation returns 404 `not_found`.
  - SQL lives only in `store.go` / `store_*.go`.
  - New endpoints follow the existing error shape and the keyset cursor format (`RFC3339Nano|uuid`, base64url raw).

## Review Focus

1. **Token expiry during polling.** A 401 from the 3 s poll must stop polling and show the sign-in screen, not loop. Test: `IncidentDetail.test.tsx › 401 while polling shows sign-in`.
2. **Route change mid-request.** A response from the previous incident must not render on the new one. Test: `IncidentDetail.test.tsx › ignores late response after navigation`.
3. **Concurrent incident numbering.** Two components opening incidents in the same project at once must get distinct, gap-free numbers. Test: `TestIncidentNumbersConcurrent` (Task 12).
4. **First revision diff.** Revision 1 has no predecessor; its "changes" must read as the initial analysis, not as "everything removed". Test: `TestRevisionHistoryFirstRevision` (Task 13).
5. **Feedback on a superseded revision's finding.** It is still accepted and shown in history, but it does not carry over to the new revision's findings. Test: `TestFeedbackDoesNotCarryAcrossRevisions` (Task 15).

## Decisions made in this plan (please check)

- **Ungroup (on "Grouped signals") is excluded.** Splitting a grouped alert out of an incident changes grouping semantics and needs its own design; the link is not rendered.
- **"Affected components" becomes "Dependencies".** Task 16 draws registered dependency edges, but Phase 1 gathers no evidence from dependencies (that is Phase 2, P2-D), so calling them "affected" would claim more than Sentinel knows. The panel is labelled **Registered dependencies**, with the note "No evidence is gathered from dependencies yet."
- **Logs and metrics in "Evidence sources"** are always listed as **Not connected** in Phase 1. Loki and Prometheus adapters are Phase 2.
- **"Incorrect" feedback** says "Marked incorrect. Recorded for review; the finding stays visible." The design's "Sent to analysis review" implied a review queue that doesn't exist.
- **Analysis steps** follow what the worker actually does, in three steps: **Collect evidence**, **Draft findings**, **Save revision**. The design's four steps included "Group signals", which happens at ingest and is always done before analysis starts, so it is shown as the first stage pill instead.
- **Home nav item** is omitted; the logo goes to the incident list. **Search (⌘K)** filters incidents by title (Task 12). **Alerts** and **Integrations** are real pages (Task 12 and Task 11).
- **Share** copies the incident URL to the clipboard and shows "Link copied".
- **Resolve** requires a note in the UI (as in the design). The API keeps it optional, so alert-driven resolution still works.

---

### Task 11: Ledger app shell, incident list and detail on today's API

PR `feat/p1-11-frontend`.

**Files:**
- Create:
  - **Config:** `frontend/package.json` (react@18.3.1, react-dom@18.3.1, vite@5.4.10, @vitejs/plugin-react@4.3.3, typescript@5.6.3, vitest@2.1.4, @testing-library/react@16.0.1, @testing-library/jest-dom@6.6.3, @testing-library/user-event@14.5.2, jsdom@25.0.1, @fontsource/geist-sans@5.3.0, @fontsource/geist-mono@5.3.0), `package-lock.json`, `vite.config.ts` (proxy `/v1` and `/healthz` to `http://127.0.0.1:${PORT:-8080}`), `tsconfig.json`, `index.html`.
  - **Entry and helpers:** `src/main.tsx`, `src/App.tsx` (hash router: `#/`, `#/incidents/:id`, `#/integrations`), `src/api.ts`, `src/format.ts`, `src/theme.ts`, `src/styles.css`.
  - **Views:** `src/views/{SignIn,IncidentList,IncidentDetail,Integrations}.tsx`.
  - **Components:** `src/components/{Sidebar,SeverityChip,StatusIcon,StagePills,Findings,FindingRow,EvidenceChip,AnalysisPanel,Timeline,Properties,ResolveDialog}.tsx`.
- Test: `src/format.test.ts`, `src/wording.test.ts`, `src/components/Findings.test.tsx`, `src/views/IncidentList.test.tsx`, `src/views/IncidentDetail.test.tsx`.

**Interfaces:**
- **Produces, `src/api.ts`:**
  - `apiFetch<T>(method: string, path: string, body?: unknown): Promise<T>` sends `Authorization: Bearer <sessionStorage "sentinel.token">`.
  - It throws `ApiError {status, code, message, requestId}`. A 401 calls `onUnauthorized()`, which `App` registers.
- **Produces, `src/format.ts`:**
  - `utcTime(iso, {seconds?}) => {text, title}`: `"14:09"` / `"14:09:12 UTC"`, with the ISO string as the title; `"–"` for null.
  - `relative(iso, now) => "2 min ago"`.
  - `gapLabel(prevIso, iso) => "5 min later" | "40 s later"`.
  - `safeHref(url) => string | null`.
  - `shortId(uuid) => "INC-" + first 8 hex` (Task 12 swaps in the number).
- **Produces, `src/components/Findings.tsx`:** `<Findings findings={Finding[]} layout="grouped"|"stream" onCite={(eventId) => void} />`.
- **Consumes:** `GET /v1/projects`, `GET /v1/projects/{id}/incidents?status&limit&cursor`, `GET /v1/incidents/{id}`, `GET /v1/incidents/{id}/timeline`, `POST /v1/incidents/{id}/reanalyze`, `POST /v1/incidents/{id}/resolve`, `GET /v1/projects/{id}/integrations`.

**Design mapping:** structure and inline styles are lifted from `sentinel-ledger.html` into CSS classes. Keep the class names `.chip .btn .btn-dark .ghost .nav .nav-on .irow .prop .frow .tag .ev .lbl .mono`.

- **Sidebar:**
  - Project switcher (the name, plus a menu when there is more than one project).
  - Incidents (open count = rows with status `open` among those loaded, until Task 12 supplies `open_count`).
  - Integrations.
  - Appearance toggle.
- **List:**
  - Header: "Incidents", "{n} shown · {open} open", "Times in UTC".
  - All/Open/Resolved segmented filter (server-side `status`).
  - Rows grouped under Open and Resolved.
  - Columns: status icon, ID, title + `component · environment`, severity chip, last seen (+ relative).
  - The Analysis and Signals columns render only when the API returns those fields (Task 12).
  - "Load more" when `next_cursor` is set.
  - Empty state: "No incidents match these filters."
- **Detail:**
  - **Top bar:** breadcrumb, Share, Resolve incident (hidden when resolved).
  - **Main column:**
    - Title.
    - **Stage pills:** Signals grouped (done); Evidence collected / Collecting evidence; Findings drafted / Findings failed; Awaiting resolution / Resolved. Their state is derived from `analysis.status` and `status`.
    - **Cause banner** when `resolution.note` is set: "Cause · recorded at resolution · {time}".
    - **Findings:** layout toggle "By type" / "Stream"; group labels **Observed**, **Hypothesis**, **Gaps**, **Check next**, with hints "backed by ingested events", "possible, not confirmed", "missing data". The kind map is `observation→Observed`, `hypothesis→Hypothesis`, `unknown→Gap`, `next_check→Check next`.
    - **Hypothesis rows:** prefixed "Possible: " with the limitations as the sub-line; the evidence row is labelled "Rests on".
    - **Evidence chips:** `↗ {type label} · {time}`, as buttons that scroll to and highlight `#evt-<id>`, or show "Event not on this page".
    - **Analysis panel:** pending = "Analysis running" + indeterminate bar; failed = "Analysis failed" + `last_error` + **Retry analysis**; none = "No analysis has run for this incident yet." + **Retry analysis**.
    - **Activity timeline:** gap labels, a type glyph (deployment `↑` accent, error `!` major, alert.triggered `◆` crit, alert.resolved `✓` ok, repeat_trigger `↻` muted), summary, source label, link-reason note, "received later · {time}" when `received_at − occurred_at > 60 s`, and the source link.
  - **Sidebar:**
    - **Properties:** Status, Severity, Component, Environment, **Investigated window**, Trigger (the trigger event's summary, linked to its row).
    - **Analysis:** Status, Revision, Completed (`latest_revision.created_at`).
    - **Grouped signals:** timeline rows with reason `trigger`/`repeat_trigger`.
  - **Resolve dialog:** copy from the design; the "Final cause" textarea is required, max 4000 characters, with a live count past 3500. A 409 shows "Already resolved" and refetches.
- **Behaviour:**
  - **Polling:** every 3 s while the analysis is `pending`/`running`, reloading the timeline when `revision_number` changes. It stops on unmount, on a terminal state and on a 401, and ignores responses from a previous route.
  - **Timeline paging:** load every page.
  - **Errors:** a timeline error appears only in the Activity section.
- **Integrations page:** a table of name, capability and status, read-only, from the existing API.

- [ ] **Step 1: Write failing tests.**
  - **`format.test.ts`:**
    - `safeHref` rejects `javascript:` and `data:`, and accepts https.
    - `utcTime` gives `"14:09"` and `"14:09:12 UTC"`, and `"–"` for null.
    - `gapLabel` gives "5 min later" and "40 s later".
  - **`Findings.test.tsx`:**
    - Kinds land under Observed/Hypothesis/Gaps/Check next.
    - A hypothesis shows "Possible: " and its limitations.
    - The stream layout has no group headers.
    - A citation button calls `onCite` with the event ID.
  - **`IncidentList.test.tsx`** (mocked fetch):
    - Rows are grouped under Open/Resolved.
    - The Resolved filter sends `status=resolved`.
    - "Load more" sends `cursor`.
  - **`IncidentDetail.test.tsx`** (mocked fetch, fake timers):
    - Properties show "Investigated window".
    - The pending panel polls every 3 s and stops when the status becomes `succeeded`.
    - Failed shows Retry, which POSTs reanalyze.
    - Timeline rows appear in order with `evt-` IDs.
    - Resolve is disabled until a note is entered, then POSTs.
    - **401 while polling shows sign-in.**
    - **Ignores a late response after navigation.**
  - **`wording.test.ts`:** no file under `src/` contains `dangerouslySetInnerHTML` or `root cause` (case-insensitive).
- [ ] **Step 2:** `cd frontend && npm test`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `npm test && npm run build`. Expected: PASS.
- [ ] **Step 5: Manual check.**
  - Start the stack with `make run` and `npm run dev`, then send the reference scenario with curl.
  - Compare against `sentinel-ledger.html` in light and dark at 1280 px and 375 px.
  - Check the pending → succeeded transition, Retry, Resolve, and Share.
  - Put screenshots in the PR.
- [ ] **Step 6:** Commit `feat(frontend): Ledger engineer view on the incident API`, then open the PR.

### Task 12: Incident numbers, list read model, filters, search and the Alerts page

PR `feat/p1-12-list-read-model`.

**Files:**
- Create: `backend/api/migrations/0005_incident_numbers.sql`, `backend/api/internal/modules/incidents/signals.go`.
- Modify: `incidents/{store.go,service.go,grouping.go,handlers.go}`, `server` routes, frontend `IncidentList.tsx`, `Sidebar.tsx`, `IncidentDetail.tsx`, `App.tsx`, `src/views/Alerts.tsx` (new), `src/components/SearchDialog.tsx` (new).
- Test: `incidents/list_test.go`, `incidents/numbers_test.go`, `incidents/signals_test.go`, `IncidentList.test.tsx`.

**Interfaces:**
- **Migration:**
  - `projects.next_incident_number bigint NOT NULL DEFAULT 1`.
  - `incidents.number bigint`, backfilled per project in `first_seen_at, id` order, then `NOT NULL` with `UNIQUE (project_id, number)`. Set `projects.next_incident_number` past the backfill.
  - `incidents.resolved_by_user_id uuid NULL REFERENCES users(id)`.
- **Numbering:** `insertOpenIncidentIfAbsent` takes the number with `UPDATE projects SET next_incident_number = next_incident_number + 1 WHERE id = $1 RETURNING next_incident_number - 1`.
  - This happens in the same transaction, only on the insert path. On `ON CONFLICT DO NOTHING` the counter update must not happen, so the counter is taken after the conflict check succeeds, in a CTE.
  - Lock order becomes advisory → project counter row → incident row → job rows. Document it in `grouping.go`.
- **List item adds:**
  - `number int64`, `analysis_status string` (`none|pending|running|succeeded|failed`, the same derivation as the detail view).
  - `signal_count int` (count of `incident_events` with reason `trigger`/`repeat_trigger`).
  - `resolution_note *string`.
- **List response adds:** `open_count int` (open incidents in the project, ignoring filters).
- **List query params:**
  - `status`.
  - `severity` (one of the four levels; otherwise 400 `invalid_severity`).
  - `component` (slug; an unknown slug returns an empty list, not an error).
  - `since` (RFC3339; otherwise 400 `invalid_since`; filters `last_seen_at >= since`).
  - `q` (1..200 runes, case-insensitive substring of the title; otherwise 400 `invalid_q`).
  - Cursor paging is unchanged.
- **Detail adds:** `number`, `signal_count`, `resolved_by {id, display_name} | null`. `Resolve` records the calling user.
- **New endpoint:** `GET /v1/projects/{projectId}/signals?limit&cursor` returns `alert.triggered` and `alert.resolved` events, newest first:
  - Each item has `{event_id, type, occurred_at, received_at, summary, severity, component {slug, display_name}, environment, disposition, incident {id, number} | null, source_url}`.
  - Limit is 1..200, default 50.
- **UI:**
  - The ID column shows `INC-{number}`; the Analysis and Signals columns are filled in.
  - The cause line shows in resolved rows ("· Cause (recorded): …").
  - Severity, Component (from `GET /v1/projects/{id}/components`) and Range (24h/7d/30d → `since`) filters are added, kept in the URL hash query.
  - The sidebar open count uses `open_count`.
  - ⌘K / Ctrl-K opens a search dialog that queries `q`.
  - New route `#/alerts` shows the signals table with links to incidents.
  - The detail shows `INC-n` and "Resolved by {display_name}" in the cause banner.

- [ ] **Step 1: Write failing tests.**
  - `TestIncidentNumbersSequentialPerProject`: numbers are 1, 2, 3 in one project and restart at 1 in another.
  - `TestIncidentNumbersConcurrent`: 10 goroutines open incidents for 2 components at once; the numbers are {1..10} with no duplicates. Run with `-race`.
  - `TestAttachDoesNotConsumeNumber`: a repeat alert doesn't advance the counter.
  - `TestListFilters`: covers each param, plus the 400 codes and `open_count`.
  - `TestListAnalysisStatusAndSignals`.
  - `TestResolveRecordsUser`.
  - `TestSignalsList`: ordering, paging, org isolation, and `incident` null for a deduplicated or evidence-only signal.
  - Frontend: filters write to the hash and send params; search sends `q`.
- [ ] **Step 2:** `make test-api` and `npm test`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Both suites pass, with `-race` on incidents.
- [ ] **Step 5:** Mutation check: remove the counter-row lock and confirm `TestIncidentNumbersConcurrent` fails. Manual check against the stack, with screenshots.
- [ ] **Step 6:** Commit and open the PR.

### Task 13: Revision history, analysis timings and evidence-source coverage

PR `feat/p1-13-revisions`.

**Files:**
- Create: `backend/api/migrations/0006_analysis_timings.sql`, `backend/api/migrations/0007_revision_events.sql` (added after review, PR #21), `analysis/history.go`, `incidents/coverage.go`.
- Modify: `analysis/{jobs.go,store.go,handlers.go}`, `backend/worker/sentinel_worker/store.py` (`lease_job` sets `started_at` on the first lease; `write_revision` / `_mark_failed` set `finished_at`), frontend `Properties.tsx`, `IncidentDetail.tsx`, `src/components/RevisionHistory.tsx` (new), `src/components/EvidenceSources.tsx` (new).
- Test: `analysis/history_test.go`, `incidents/coverage_test.go`, `backend/worker/tests/test_store.py`, `IncidentDetail.test.tsx`.

**Interfaces:**
- **Migrations:**
  - `0006`: `analysis_jobs.started_at timestamptz NULL`, `analysis_jobs.finished_at timestamptz NULL`.
  - `0007`: `revision_events (revision_id, event_id)`, the events each revision's analysis read. The worker writes them in the same transaction as the revision.
- **`analysis.View` adds:** `started_at`, `finished_at` (from the job shown), `revision_count int`.
- **`GET /v1/incidents/{id}/revisions`** returns `{items: [...]}` newest first (no paging; revisions per incident are few). Each item has:
  - `revision_number`, `created_at`.
  - `reason` (`incident_created|repeat_trigger|late_event|manual_retry`, from the job).
  - `finding_count`.
  - `changes {added: [{kind, rule_id}], removed: [{kind, rule_id}]}`, a multiset diff of `(kind, rule_id)` against the previous revision. For revision 1, `changes` is `null`.
  - `new_evidence: [{event_id, type, received_at}] | null`: the events this revision read (from `revision_events`) that no earlier revision read; for revision 1, everything it read. Ordered by `received_at`.
    - `null` means unknown: the revision was written before `revision_events` existed, or only such revisions came before it.
    - *Changed after review (PR #21):* the first version compared `linked_at` with job `started_at` windows. Retries, overlapping jobs and clock skew between the API and the database all misplaced evidence that way.
- **`GET /v1/incidents/{id}/revisions/{n}`** returns the full revision (`id, revision_number, created_at, findings`), or 404 `not_found`.
- **Detail adds `evidence_sources`:**
  - One entry per project integration: `{integration_id, name, capability, event_count, status}`.
  - `status` is `retrieved` when `event_count > 0`, else `no_matching_data`.
  - It is followed by fixed entries `{name: "Logs", status: "not_connected"}` and `{name: "Metrics", status: "not_connected"}`.
- **UI:**
  - The Analysis section gets Started / Completed or Failed / Elapsed (live while running), and Revision `n` with **History**.
  - The history list shows reason text ("Initial analysis when the incident opened.", "Repeat alert.", "Late evidence: {type} received at {time}.", "Retried by an engineer."), the changes line ("+ Observed, − Gap", or "Initial analysis") and View.
  - Viewing an old revision shows the design's "Viewing Revision n, superseded at …" banner with "Back to Revision m".
  - The Evidence sources panel has the coverage bar and the "{k} of {n} returned data" label.

- [ ] **Step 1: Write failing tests.**
  - `TestRevisionHistoryFirstRevision`: `changes` is null and `new_evidence` lists every event revision 1 read.
  - `TestRevisionHistoryLateDeployment`: revision 2 has reason `late_event`, adds `deployment_in_window`, `deployment_near_onset`, `compare_by_version` and `review_change`, and removes `no_deployment_evidence` and `check_change_history`, with `new_evidence` = [the deployment].
  - After review: `TestRevisionHistoryRetrySeesLateEvent`, `TestRevisionHistoryOverlappingJobs`, `TestRevisionHistoryNotNewTwice`, `TestRevisionHistoryLegacyRevisions`.
  - `TestRevisionByNumber404`, including another organisation.
  - `TestCoverage`.
  - Worker: `test_started_and_finished_at`, where `started_at` is not overwritten on a re-lease.
  - Frontend: the History panel and the old-revision banner.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** All three suites pass.
- [ ] **Step 5:** End to end: alert, then a late deployment. Revision history shows both revisions with the right diff. Add screenshots to the PR.
- [ ] **Step 6:** Commit and open the PR.

### Task 14: Analysis steps and a deterministic, cited summary

PR `feat/p1-14-summary-steps`.

**Files:**
- Create: `backend/api/migrations/0007_summary_steps.sql`, `backend/worker/sentinel_worker/summary.py`, `backend/worker/tests/test_summary.py`.
- Modify: `sentinel_worker/{main.py,store.py,models.py}`, `analysis/{jobs.go,store.go}`, frontend `AnalysisPanel.tsx`, `IncidentDetail.tsx`, `src/components/Summary.tsx` (new).
- Test: `test_summary.py`, `test_main.py`, `analysis/jobs_test.go`, `IncidentDetail.test.tsx`.

**Interfaces:**
- **Migration:**
  - `analysis_jobs.step text NULL CHECK (step IN ('collect_evidence','draft_findings','save_revision'))`.
  - `analysis_jobs.step_started_at timestamptz NULL`.
  - `analysis_revisions.summary jsonb NOT NULL DEFAULT '[]'`.
- **Worker:**
  - `store.set_step(conn, job, step) -> bool` is lease-guarded.
  - `process_one` calls it before `load_context` (`collect_evidence`), before `analyze` (`draft_findings`) and before `write_revision` (`save_revision`). On failure the step stays where it failed.
- **`summary.summarize(ctx: IncidentContext, findings: list[Finding]) -> list[Segment]`:**
  - `Segment = {"text": str, "kind": "text" | "time" | "strong"}`.
  - The summary is built only from the given findings:
    - The trigger observation.
    - Then each `deployment_in_window` and `errors_observed` observation, in order.
    - Then "Investigate …", from the first next check.
    - When there is no deployment evidence: "Deployment information unavailable." followed by a `strong` segment "Cause: Unknown."
  - It is deterministic, and the banned-words test applies.
- **`write_revision`** stores the summary in the same transaction.
- **API:**
  - `analysis.View` adds `step`, `step_started_at`.
  - `Revision` adds `summary` (array of segments).
- **UI:**
  - The Summary section has the left accent bar and "Built only from the {n} findings below" (or "Not available yet").
  - The pending panel lists the three steps with done/running/queued and the step meta.
  - The failed panel names the step it failed at ("Analysis failed at step 2 of 3: Draft findings") and `last_error`.

- [ ] **Step 1: Write failing tests.**
  - `test_summary.py`:
    - The reference scenario gives the exact segments.
    - Alert-only includes "Cause: Unknown." as `strong`.
    - Every time segment matches a finding time.
    - Deterministic.
    - No banned words.
  - `test_main.py`:
    - Steps progress in order.
    - A crash in `analyze` leaves `step = 'draft_findings'`.
  - Go: the View exposes `step` and `summary`.
  - Frontend: the summary renders `time` segments in mono and `strong` in bold, and the failed panel shows the step name.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Mutation check: drop a citation from the summary and confirm the "time segment matches a finding" test fails. End to end with screenshots.
- [ ] **Step 6:** Commit and open the PR.

### Task 15: Finding feedback and next-check tracking (PRD UI-05)

PR `feat/p1-15-feedback`.

**Files:**
- Create: `backend/api/migrations/0008_feedback.sql`, `backend/api/internal/modules/feedback/{service.go,store.go,handlers.go}`, `feedback/feedback_test.go`.
- Modify: `analysis/{jobs.go,store.go}` (finding IDs and feedback in the read model), `server` routes, the `archtest` allow-list if needed, frontend `FindingRow.tsx`, `api.ts`.
- Test: `feedback_test.go`, `Findings.test.tsx`.

**Interfaces:**
- **Migration:**
  - `finding_feedback (finding_id uuid REFERENCES findings ON DELETE CASCADE, user_id uuid REFERENCES users, verdict text CHECK (verdict IN ('useful','incorrect')), updated_at timestamptz, PRIMARY KEY (finding_id, user_id))`.
  - `next_check_status (finding_id uuid PRIMARY KEY REFERENCES findings ON DELETE CASCADE, done_by_user_id uuid REFERENCES users, done_at timestamptz)`.
- **Endpoints:**
  - `PUT /v1/findings/{findingId}/feedback {verdict}` → 200 with the finding's feedback. An invalid verdict returns 400 `invalid_verdict`.
  - `DELETE /v1/findings/{findingId}/feedback` → 204.
  - `PUT /v1/findings/{findingId}/check {done: bool}` → 200. A finding that isn't `next_check` returns 400 `not_a_check`.
  - Every endpoint is organisation-scoped through finding → revision → incident.
- **`analysis.Finding` adds:**
  - `id`.
  - `feedback {useful int, incorrect int, mine "useful"|"incorrect"|null}`.
  - `check {done bool, done_by {id, display_name} | null, done_at} | null` (next_check only).
- **UI:**
  - Useful / Incorrect toggles per finding (clicking again clears it) with optimistic update and rollback on error.
  - The incorrect note reads "Marked incorrect. Recorded for review; the finding stays visible."
  - Next-check checkboxes show strike-through and "Done by {name}" in the title.
  - Feedback is shown on old revisions too (read-write).

- [ ] **Step 1: Write failing tests.**
  - `TestFeedbackSetChangeClear`.
  - `TestFeedbackOtherOrg404`.
  - `TestCheckOnlyNextCheck`.
  - `TestFeedbackDoesNotCarryAcrossRevisions`.
  - `TestFeedbackCounts` across two users.
  - Frontend: toggle, rollback on 500, and the checkbox sends `done`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Manual check. **Step 6:** Commit and open the PR.

### Task 16: Registered component dependencies

PR `feat/p1-16-dependencies`.

**Files:**
- Create: `backend/api/migrations/0009_component_dependencies.sql`, `tenancy/dependencies.go`, `tenancy/dependencies_test.go`, frontend `src/components/Dependencies.tsx`.
- Modify: `tenancy/{store.go,handlers.go}`, `incidents/service.go` (detail field), `support/seed/seed.go` (adds the `payment-provider` component and the edges `checkout-api → payment-service` and `payment-service → payment-provider`).
- Test: `dependencies_test.go`, `seed_test.go`, `IncidentDetail.test.tsx`.

**Interfaces:**
- **Migration:**
  - `components.kind text NOT NULL DEFAULT 'internal' CHECK (kind IN ('internal','external'))`.
  - `component_dependencies (project_id, from_component_id, to_component_id, created_at, PRIMARY KEY (from_component_id, to_component_id), CHECK (from_component_id <> to_component_id))`.
  - Both components must be in the same project and environment.
- **Endpoints:**
  - `GET /v1/projects/{projectId}/dependencies`.
  - `POST /v1/projects/{projectId}/dependencies {from, to}` (component IDs) → 201, or 409 `duplicate`. A cross-project or cross-environment pair, or a self edge, returns 400 `invalid_dependency`.
  - `DELETE /v1/projects/{projectId}/dependencies/{fromId}/{toId}` → 204.
  - Component create accepts an optional `kind`.
- **Detail adds:** `dependencies: [{component {id, slug, display_name, kind}, depth: 1|2, via_component_id}]`, outgoing edges from the incident's component, two hops at most, cycle-safe.
- **UI:** the "Registered dependencies" panel from the design (indented `↳` rows; meta "calls" for internal, "external" for external), with the note "No evidence is gathered from dependencies yet."

- [ ] **Step 1: Write failing tests.**
  - `TestDependencyValidation`.
  - `TestDependencyTwoHopsCycleSafe`, with a cycle a→b→a.
  - `TestDependenciesOtherOrg404`.
  - The seed creates the edges idempotently.
  - Frontend: the panel and its note.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Manual check. **Step 6:** Commit and open the PR.

### Task 17: One-command stack, demo, metrics, docs

The former Task 12, unchanged except:
- The demo script also posts a late deployment so that revision history (Task 13) is visible.
- The README's screenshots are of the Ledger view.
