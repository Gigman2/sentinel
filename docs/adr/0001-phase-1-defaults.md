# ADR 0001: Phase 1 defaults

**Status:** accepted · **Date:** 2026-11-10

## Context

The Phase 1 scope (`docs/scope/Sentinel_Phase_1_Core_Investigation.md`) leaves many numbers and behaviours open. The plans in `docs/plans/` chose them so the system could be built and tested. This record collects those choices in one place, each with the signal that should make us look at it again.

## Decisions

### Evidence and ingest

- **Evidence window:** an incident investigates from 30 minutes before its first alert to 15 minutes after its last one (`incidents.WindowBefore` / `WindowAfter`). Deployments and errors for the same component and environment in that window are linked.
  *Revisit when* engineers report relevant evidence just outside the window, or windows on noisy components link too much.
- **Timestamp limits:** an event more than 5 minutes in the future, or more than 30 days in the past, is rejected (`timestamp_in_future`, `timestamp_too_old`).
  *Revisit when* a real source sends from badly skewed clocks, or a backfill of older history is needed.
- **Payload limits:** 64 KiB per request and at most 50 attribute keys. String lengths are counted in runes.
  *Revisit when* a real integration needs larger payloads.
- **Capability split:** an integration is either `incident_input` (alerts and errors) or `change_source` (deployments). It can send only its own event types (403 otherwise).
  *Revisit when* one real tool needs to send both kinds.
- **Replays first:** a resent `source_event_id` is answered from storage (200, same `event_id`) before timestamps or mappings are checked again, so a sender's retry after a clock change still gets its original answer.
  *Revisit when* replays should be revalidated.
- **Rate limit:** 20 requests per second with a burst of 40, per integration, in memory. Organisation-wide limits are deferred to Phase 2.
  *Revisit when* the API runs as more than one process, or one organisation's integrations crowd out another's.

### Incidents

- **Severity escalation:** a repeat alert raises the incident's severity to the highest seen and never lowers it. `first_seen_at` never moves.
  *Revisit when* engineers want severity to follow the latest alert.
- **Out-of-order resolve:** an `alert.resolved` that occurred before the incident's last alert still resolves it.
  *Revisit when* flapping alerts close incidents that are still firing.
- **Late events:** any evidence event inside an incident's window triggers reanalysis, whatever the incident's status (including resolved).
  *Revisit when* reanalysing resolved incidents creates noise.
- **Per-component advisory lock:** grouping alerts and linking evidence are serialized per component with a transaction-scoped advisory lock, so concurrent alerts open one incident and no event misses a window.
  *Revisit when* one component's ingest rate makes the lock a bottleneck.

### Analysis jobs

- **Coalescing:** at most one pending job per incident; new reasons fold into it.
- **Retries:** 5 attempts, with backoff `min(5 s × 2^attempts, 300 s)`.
- **Lease:** 60 seconds. Writes to a job are guarded by its lease token.
- **Crash sweep:** an expired lease at max attempts is marked `failed`.
- **Superseded rule:** a retry that would collide with a newer pending job fails as superseded instead; the pending job analyses everything.

*Revisit when* analysis takes long enough to outlive the lease, or failures need different backoff.

### Access and exposure

- **Metrics:** `GET /metrics` is unauthenticated. From the host it is reachable only through the loopback-bound API port. Inside the Compose network the API listens on every interface, so any container on that network can read it; `make run` with an empty `HOST` exposes it on every host interface. Nothing it shows is tenant data (counts and an age), but each scrape queries the database.
  *Revisit when* the API is exposed beyond the local machine (Phase 3), or untrusted containers join the Compose network.
- **Loopback-only ports:** every port Compose publishes binds to `127.0.0.1`. The nginx front end does not proxy `/metrics`.
  *Revisit when* the stack is deployed anywhere shared.
- **Single-membership tokens:** a user token is rejected (401 `ambiguous_membership`) when its user belongs to more than one organisation, until tokens carry an organisation.
  *Revisit when* one person needs access to several organisations.
- **Demo tokens:** `.env.example` holds fixed, clearly labelled demo tokens so `make up && make demo` works with no setup. They are for the local stack only.
  *Revisit when* any non-local deployment exists: it must generate its own.

- **Demo incidents:** `make up` and `make run` run `sentinel-api demo-data` after the seed, filling the demo tenant with incidents that exercise every part of the UI. It is on by default for these local stacks (`SEED_DEMO_DATA=0` turns it off); the `seed` command and the tests never add it.
  *Revisit when* any non-local deployment exists: it must not run there.

### Front end

- **React + Vite, served by nginx:** the engineer view is a static build. nginx serves it with a strict same-origin Content-Security-Policy and proxies `/v1` and `/healthz` to the API. `index.html` and unhashed files are revalidated on every load (`no-cache`), and hashed `/assets/` files are cached for a year (`immutable`).
  *Revisit when* the UI needs server rendering or a separate origin from the API.
