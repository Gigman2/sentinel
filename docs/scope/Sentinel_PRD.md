# Sentinel — Product Requirements Document

**Version:** 1.0 working draft  
**Date:** 8 November 2026  
**Owner:** Gigman  
**Status:** Ready to implement the first vertical slice  
**Companion documents:** *Sentinel — North Star* (28 September 2026) and *Sentinel — 6-Month Engineering Curriculum* (13 September 2026)

## 1. Executive summary

Sentinel is an incident investigation platform for engineering teams. An alert, application event, or later a defined polling rule can start an investigation. Sentinel identifies the affected component, gathers relevant evidence from connected systems, orders it in a timeline, and presents observations, possible explanations, gaps, and suggested next checks. Its purpose is to reduce the engineer's **time to understanding**.

The core loop is **Ingest → Correlate → Investigate → Explain**. The first release implements this loop with a generic incident webhook, a deployment event webhook, PostgreSQL, a Go API, a small Python analysis worker, and a minimal incident view. It does not require an LLM, a message broker, or a full observability stack.

Sentinel is not the primary monitor or log/metric store. It works with existing sources. It does not assert that a nearby deployment caused an incident merely because it happened first.

## 2. Problem, user, and outcome

### Problem

When checkout begins failing, the on-call engineer may find the alert in one system, logs in another, metrics in a third, and deployments in CI/CD. They need to establish what happened, when it began, which component is affected, what changed, and what evidence supports each lead. Collecting this context is repetitive and slow.

### Initial user

An engineer responsible for a small or medium production system with multiple services. They can configure integrations and investigate incidents. A second role, an organization admin, can manage membership and connection credentials.

### Primary job to be done

> When an incident is reported for a component I operate, show me the relevant facts and their sources, distinguish possible causes from proven facts, and tell me what to inspect next.

### Success measures

The first build is evaluated using a scripted incident exercise, not an invented business KPI:

| Measure | How to assess it |
| --- | --- |
| Time to first useful lead | Compare the same exercise with and without Sentinel; record the baseline before setting a target |
| Evidence provenance | Every displayed finding has a source event or query reference, except explicit gaps and next steps |
| Incident grouping quality | A repeated alert updates the intended incident; an unrelated component does not join it |
| Analyst calibration | The output distinguishes observation, hypothesis, and unknown; reviewers can reject unsupported claims |
| Reliability | Duplicate submissions and worker restarts do not lose an accepted incident or create duplicate findings |

## 3. Product vocabulary and boundaries

| Term | Definition |
| --- | --- |
| Organization | Tenant and authorization boundary |
| Project | Collection of related components within an organization |
| Component | Operated service or dependency, such as `checkout-api`, `payment-service`, or a payment provider |
| Integration | Configured connection to an external system, with one or more capabilities |
| Signal | Incoming assertion that something occurred, including an alert, error summary, or deployment |
| Incident | Sentinel's durable investigation record, possibly grouping multiple related alert signals |
| Evidence item | Source-linked fact relevant to an incident, ingested or obtained by a bounded query |
| Finding | Structured observation, hypothesis, gap, or recommended next check derived from evidence |

**Integration capabilities:**

1. **Incident input:** Push alerts or, later, pull a defined detection source. It supplies candidate incident signals. A signal is not automatically a distinct incident.
2. **Evidence source:** Query logs, metrics, traces, or history after an incident is known. Loki and Prometheus are planned examples.
3. **Change source:** A specialized evidence source for deployments, commits, and configuration changes. GitHub, GitLab, Bitbucket, and CI systems are examples. In the first release, deployment facts arrive through a generic webhook.
4. **Notification destination:** Outbound delivery of incident updates, such as email or Slack. It is not needed for the first end-to-end slice.

One provider can have several capabilities. The provider is *how* Sentinel connects; the capability is *what the connection does*. Configuration is scoped to an organization and project; explicit mappings associate provider identifiers and environments with Sentinel components. Incoming payloads cannot select a different tenant.

### Product boundaries

- Sentinel integrates with monitoring; it does not start as an uptime monitor.
- Sentinel queries selected telemetry around an incident; it does not continuously replicate all logs and metrics.
- Sentinel recommends investigation, but never automatically rolls back or edits production in the first releases.
- Temporal proximity and a changed commit produce a **candidate lead**, not a confirmed root cause.
- Tenant isolation, credential scoping, and provenance are foundational even in a one-organization demo.

## 4. Reference incident: checkout degradation

**Demo environment:** `Uppsel Production` project, `checkout-api` calling `payment-service`; `payment-service` calls an external payment provider. The example is synthetic and does not require access to Uppsel's real systems.

| Time (UTC) | Source fact | Sentinel's treatment |
| --- | --- | --- |
| 14:02 | `payment-service` version `1.8.4` deployed | Change event, potential context |
| 14:07 | Payment requests begin timing out | Error evidence if supplied |
| 14:09 | Alert reports elevated checkout failures | Incident input; create or attach incident |
| 14:10 | Repeated delivery of the same alert | Idempotent acknowledgement; no duplicate event |
| 14:11 | Engineer opens the incident | Timeline, evidence, gaps, and next checks visible |

The analysis may say: “Checkout failures rose at 14:09. A payment-service deployment occurred at 14:02, and timeout events were observed at 14:07. Investigate the payment integration and compare affected instances by deployed version.” It must **not** say that commit `abc123` caused the outage without stronger evidence.

In the missing-deployment variant, it says that deployment information is unavailable and leaves the cause unknown.

## 5. User journeys

### 5.1 Set up a project

1. An admin creates an organization and project.
2. They register `checkout-api` and `payment-service` under the project, selecting `production` as the environment.
3. They create an incident webhook integration and a change webhook integration. Sentinel shows the endpoint, a credential once, and sample payloads.
4. They map each source's component identifier to a registered component. Unmapped events are visible as rejected or unresolved intake records, not silently associated by a guessed name.
5. They send a test event and see the validation result.

For the first local demo, seed an admin and organization instead of building a full signup/email flow. The API and database must still enforce organization ownership.

### 5.2 Investigate an incident

1. An authorized source sends an alert signal for `checkout-api` in `production`.
2. Sentinel verifies the source, normalizes the event, persists it, and creates or matches an incident.
3. Sentinel starts analysis and links relevant known events within the initial time window.
4. The engineer sees an incident summary, timeline, affected components, source links, findings, and explicit data gaps.
5. Later evidence can update the incident and trigger a new analysis revision without erasing earlier findings.
6. The engineer may mark the incident resolved and record a human conclusion. Automated resolution synchronization is a later capability.

### 5.3 Integration failure

If the Python worker or an external source is unavailable, the incident and accepted events remain queryable. The incident view shows `analysis pending` or a specific source failure; a retry can complete enrichment. An empty query result is described as “no matching data found,” not “component healthy.”

## 6. Scope and release plan

| Release | Must deliver | Explicitly deferred |
| --- | --- | --- |
| **V0: working vertical slice** | Seeded tenant/admin; project and component API; generic authenticated alert and deployment webhooks; deduplication; incident grouping; PostgreSQL persistence; durable analysis job; Python rule-based worker; incident list/detail/timeline API and minimal view; local Docker; synthetic checkout scenario | Native Sentry/GitHub/Loki/Prometheus adapters, LLM, Slack, OAuth, full RBAC UI, Kubernetes |
| **V1: credible investigation** | A native incident connector; read-only Loki and Prometheus evidence adapters; explicit component-label mapping; bounded query recipes; analyst feedback; notifications; deployment and prior-incident context | General-purpose telemetry warehousing, automatic remediation |
| **Later** | More providers; dependency graph; historical retrieval; AI grounded in evidence; evaluations; scalable processing; cloud and security hardening | Any feature without a demonstrated incident-investigation benefit |

V0 is the immediate build target. The V1 items are product direction, not prerequisites for writing the first Go handler.

### Four-phase product and learning roadmap

These phases describe **what you build while learning**, rather than four independent rewrites. Each phase leaves a working Sentinel behind. The month labels follow the existing six-month curriculum; since your Go lessons are complete and Python is in progress, treat them as study blocks rather than new calendar deadlines. The V0/V1 labels above describe product maturity and can span phase boundaries.

| Phase | Learning-plan alignment | Sentinel capability and build work | Exit demonstration |
| --- | --- | --- | --- |
| **1. Core investigation** | **Month 1: Go + Python.** Finish the Python refresher through the parts needed for the worker while putting Go into practice. | Build the organization → project → component model, generic authenticated alert and deployment ingestion, idempotent incident grouping, timeline, PostgreSQL job handoff, rule-based Python analysis and a minimal incident view. Begin with the Step 1 vertical cut in Section 13; add the rest incrementally. | Run the synthetic checkout scenario end to end. Duplicate and late events behave predictably; an alert without supporting evidence yields an honest “unknown.” |
| **2. Real evidence and distributed design** | **Months 2–3: system design; AWS foundation begins in Month 3.** Study queues, delivery, consistency, backpressure, caching, scaling and failure modes against this running system. | Add one native incident provider, bounded read-only Loki and Prometheus queries, component-to-source mappings, change context and one notification destination. Test concurrent ingestion and slow/failed connectors. Introduce a broker or cache only when a measured workload or failure case warrants it. Document architecture decisions and cloud requirements. | A real provider alert opens an incident; Sentinel retrieves source-linked logs/metrics and deployment context, still works when one connector is unavailable, and explains its grouping and retry behavior. |
| **3. Production operations** | **Month 4, with groundwork in Month 3: AWS, Terraform, CI/CD and Solutions Architect preparation.** | Containerize and deploy the existing product with reproducible infrastructure, managed persistence, secrets, backups, least-privilege access, CI/CD, structured logs, metrics, traces and a rollback/recovery procedure. Keep the deployment cost visible. | A deployed environment processes a test incident; a failed worker recovers without losing accepted events; the team can trace an event from ingestion through analysis and restore from the documented backup path. |
| **4. Evidence-grounded intelligence and hardening** | **Months 5–6: AI engineering, then AppSec and integration.** | Add historical incident/runbook retrieval and structured AI findings that cite evidence and express uncertainty. Evaluate against scripted incidents, including missing and misleading data. Threat-model the complete platform and address tenant isolation, connector credentials, SSRF, prompt injection, malicious logs, auditability and abuse. Expand providers only where they improve investigation. | On a held-out incident set, findings are useful and source-linked; unsupported claims are detected in evaluation. Cross-tenant and adversarial scenarios pass, and a human can inspect the evidence before acting. |

**Progression rule:** do not delay Phase 1 for course completion. Use each lesson against a concrete Sentinel problem, and do not add Kafka, Kubernetes, a vector database or an autonomous agent simply to match a course topic. Security basics and tenant scoping apply from Phase 1; Phase 4 is the dedicated review and hardening block.

## 7. V0 functional requirements

Requirements labeled `P0` block the first usable release; `P1` is the next increment.

### 7.1 Tenant, project, and component setup

| ID | Priority | Requirement and acceptance criterion |
| --- | --- | --- |
| SET-01 | P0 | Seed or create an organization and admin membership. Every read and write is scoped by authenticated organization, never by a trusted request-body `organization_id`. |
| SET-02 | P0 | Create/list projects with unique slugs within an organization. A project in another organization is inaccessible. |
| SET-03 | P0 | Create/list components with project, environment, name, optional description and owner. A component belongs to exactly one project. |
| SET-04 | P0 | Configure a generic integration with capability, project scope, credential, status and explicit external-component mapping. A disabled integration cannot ingest. |
| SET-05 | P1 | Maintain dependency edges between components and use them to widen evidence search in a controlled way. |

### 7.2 Intake and event normalization

| ID | Priority | Requirement and acceptance criterion |
| --- | --- | --- |
| ING-01 | P0 | Accept `alert.triggered`, `alert.resolved`, `error.observed`, and `deployment.completed` through a versioned generic webhook contract. |
| ING-02 | P0 | Authenticate integration credential; enforce project/environment/component mapping; reject unauthorized or unmapped events with a clear, non-sensitive reason. |
| ING-03 | P0 | Persist normalized type, occurrence time, receipt time, external event ID, source, component, environment, severity, sanitized attributes, and a source reference. |
| ING-04 | P0 | Enforce unique `(integration_id, source_event_id)`. Replays return the existing Sentinel event ID and do not repeat side effects. |
| ING-05 | P0 | Validate supported type, timestamp skew policy, payload size and required type-specific fields. Never log credentials or unredacted sensitive payloads. |
| ING-06 | P1 | Show intake health and rejected/unmapped counts so users know when configuration is broken. |

**Illustrative V0 payload:**

```json
{
  "schema_version": "1",
  "source_event_id": "alert-7209",
  "type": "alert.triggered",
  "occurred_at": "2026-11-08T14:09:00Z",
  "external_component_id": "checkout-prod",
  "environment": "production",
  "severity": "critical",
  "fingerprint": "checkout-error-rate-high",
  "summary": "Checkout error rate exceeded threshold",
  "source_url": "https://monitor.example/alerts/7209",
  "attributes": {"error_rate": 0.18}
}
```

The integration credential identifies organization/project. `external_component_id` is resolved through configured mapping. A deployment uses `deployment.completed` plus `version`, `deployed_at` (or `occurred_at`), and optionally `commit_sha`, `repository`, `deployment_id`, and `source_url`. A value supplied by a source is evidence about that source, not proof that code caused an incident.

### 7.3 Incident lifecycle

| ID | Priority | Requirement and acceptance criterion |
| --- | --- | --- |
| INC-01 | P0 | A new `alert.triggered` creates an incident with `open` status, primary component, severity, first/last seen, and source fingerprint. |
| INC-02 | P0 | Match subsequent alerts to an open incident on organization, project, environment, primary component and fingerprint. A different component or fingerprint creates a separate incident in V0. |
| INC-03 | P0 | The same source event never creates another incident; a distinct repeat alert matching the key attaches to the existing incident and updates last seen. |
| INC-04 | P0 | `alert.resolved` with matching key marks the incident resolved only if an open incident exists; otherwise retain the event and surface it as unmatched. Manual resolution is also available. |
| INC-05 | P0 | `deployment.completed` and `error.observed` are stored as candidate evidence; they never create an incident on their own in V0. |
| INC-06 | P1 | Add configurable grouping windows and cross-component incident grouping after evaluating cases where V0 over- or under-groups. |

**V0 grouping assumption:** one open incident per exact key. The uniqueness must be transactionally enforced or serialized so concurrent webhooks cannot create two. A new alert after resolution opens a new incident. Late events can enrich an existing incident if they fall within its evidence window, but cannot silently rewrite its incident start time. Record any exception as an explicit rule change.

### 7.4 Evidence and investigation

| ID | Priority | Requirement and acceptance criterion |
| --- | --- | --- |
| EVD-01 | P0 | For an incident, examine stored events for its component and environment from 30 minutes before first seen through 15 minutes after last seen; make this window configurable later. |
| EVD-02 | P0 | Link selected events to the incident with reason (`trigger`, `same_component_time_window`, etc.), provenance and query window. Do not imply causality from a link. |
| EVD-03 | P0 | Persist analysis revisions. Each revision contains typed observations, hypotheses, unknowns and suggested next checks. Every factual claim cites one or more evidence IDs. |
| EVD-04 | P0 | Python worker processes a durable `incident.created` job, assembles a timeline and writes structured output. If the job runs again, it does not create duplicate revision content for the same job/revision key. |
| EVD-05 | P0 | A late deployment or error event within the window schedules reanalysis. Expose latest revision and revision timestamp; do not silently replace the underlying events. |
| EVD-06 | P1 | Add read-only Loki log and Prometheus metric query recipes per component. Each query has bounded time, selectors, result/series limits, timeout, and an auditable source reference. |

**Finding contract (illustrative):**

```json
{
  "kind": "hypothesis",
  "text": "The payment deployment may be related to checkout failures.",
  "evidence_ids": ["evt_deploy_1", "evt_alert_1"],
  "limitations": "Timing alone cannot establish causation."
}
```

V0 rule-based analysis can produce “deployment near onset,” “error observed before alert,” and “no deployment evidence available.” It should never manufacture a root cause.

### 7.5 Engineer view

| ID | Priority | Requirement and acceptance criterion |
| --- | --- | --- |
| UI-01 | P0 | List incidents by project with status, severity, affected component and last seen; newest first. |
| UI-02 | P0 | Detail view displays summary, source trigger, ordered timeline, source links, analysis status, latest findings and evidence gaps. |
| UI-03 | P0 | The interface clearly distinguishes observed facts, hypotheses and actions to investigate. It does not use “confirmed root cause” for an unverified hypothesis. |
| UI-04 | P0 | Show pending/failed analysis and allow a retry via an authorized endpoint. |
| UI-05 | P1 | Capture engineer feedback on useful/incorrect findings and a human-written final cause. |

The first UI can be minimal React + Vite or even an API-first implementation with a simple incident page. A polished dashboard is not a V0 gate.

## 8. V1 evidence adapters and notifications

**Loki:** configure read-only endpoint/auth, tenant settings where applicable, label selector per component/environment, and bounded LogQL recipes. Query only around an incident; store selected snippets, timestamps, query reference and retrieval status rather than a copy of the entire log stream. Mask secrets and sensitive fields before showing or passing to analysis.

**Prometheus:** configure read-only endpoint/auth and curated PromQL recipes for error rate, latency and availability. Record the expression, time window, step and result summary. A missing series is “no data,” not “healthy.” Protect the remote query service with timeouts and concurrency limits.

**Change sources:** start with ingested deployment events. Native Git provider/CI connectors can later retrieve deployment metadata and commit ranges. Rank candidate changes by affected component, deployment/incident time, version distribution and supporting telemetry. Multiple deployments remain multiple candidates until stronger evidence differentiates them.

**Notifications:** send an outbound incident-created or analysis-ready message to configured recipients/destinations. Delivery failure does not roll back incident creation. Prevent duplicate sends on retries; expose delivery status. Start with one provider only after V0 works.

## 9. Proposed APIs and contracts

These paths are a first contract for implementation review, not a commitment to a framework:

| Method/path | Purpose |
| --- | --- |
| `POST /v1/projects` | Create project in authenticated organization |
| `GET /v1/projects` | List accessible projects |
| `POST /v1/projects/{projectId}/components` | Register component |
| `GET /v1/projects/{projectId}/components` | List components |
| `POST /v1/projects/{projectId}/integrations` | Configure webhook integration and mappings |
| `POST /v1/ingest/{integrationId}/events` | Submit one external event using integration credential |
| `GET /v1/projects/{projectId}/incidents` | List incidents with filters and pagination |
| `GET /v1/incidents/{incidentId}` | Incident summary and latest analysis |
| `GET /v1/incidents/{incidentId}/timeline` | Ordered, paginated evidence timeline |
| `POST /v1/incidents/{incidentId}/reanalyze` | Authorized analysis retry |
| `POST /v1/incidents/{incidentId}/resolve` | Human resolution |

**Response principles:** `201` for a newly accepted event, `200` for a replay with the existing ID, `400` for invalid schema, `401/403` for invalid/insufficient credentials, `422` for an unmapped component, and `429` for rate limiting. Exact status distinctions can be standardized during API implementation. All errors have a stable machine-readable code and request ID; do not echo secret values.

## 10. Data model and consistency

Minimum tables or equivalent relational records:

- `organizations`, `users`, `memberships`, `projects`, `components`
- `integrations` and `integration_component_mappings` (credential hash/reference, scope, capability, enabled state)
- `operational_events` (source ID unique per integration, normalized fields, safe raw attributes, received time)
- `incidents` and `incident_events` (incident grouping key, lifecycle, event links and relevance reason)
- `analysis_jobs`, `analysis_revisions`, `findings`, `finding_evidence`

Use foreign keys and tenant-aware query predicates. The incident grouping key and external source event key need database uniqueness guarantees, not only in-memory checks. Creating an incident and recording a durable analysis job should be one transaction, or an outbox should guarantee that an accepted incident can later be analyzed. A database-backed job with lease, retry count, next-attempt time and terminal failure state is sufficient for V0; introduce a broker when scaling or independent consumers justify it.

**Retention assumption for V0:** keep synthetic/demo events until manually cleared. Before real customer data, define per-tenant retention and deletion policies, access auditing and data minimization. Avoid storing raw log bodies by default.

## 11. Security, privacy and operational requirements

- All human reads/writes require authenticated organization membership; integration credentials are separately scoped and revocable. Do not use a payload-provided organization ID for authorization.
- Store credential hashes where possible; encrypt retrievable upstream tokens, restrict their use to the connector, and redact them from logs and API responses.
- Future user-configured outbound connector URLs are SSRF-sensitive. Restrict schemes and destinations, resolve and validate addresses, block private/metadata ranges unless explicitly supported through a controlled deployment model, and account for redirects and DNS changes.
- Bound payload size, query window, result count, retries and worker concurrency. Rate-limit by integration and organization.
- Preserve source time and receipt time; represent missing, late and failed data explicitly. UTC internally; display time zones in UI.
- Instrument ingestion success/rejection, job lag/retry/failure, query latency, analysis duration and incident counts. Use request/incident/job IDs in structured logs.
- V0 local environment starts with one command and includes sample data and migrations. It must survive an API or worker restart without losing accepted work.

## 12. Acceptance suite

The release is complete only when the following scenarios can be demonstrated:

1. **Happy path:** seeded tenant, project and components; deployment + error + alert events create one incident, timeline and evidence-linked findings.
2. **Duplicate delivery:** resend the same `source_event_id`; no additional operational event, incident or analysis job is created.
3. **Repeat alert:** send a distinct alert event with the same grouping key while open; it updates the incident rather than making another.
4. **Different scope:** same fingerprint for a second component or organization does not join the first incident; cross-tenant reads fail.
5. **Out-of-order arrival:** deployment received after alert but with earlier occurrence time appears correctly in the timeline and produces a new analysis revision.
6. **Missing evidence:** alert alone gives an incident with explicit uncertainty and next checks; no invented deployment or cause.
7. **Worker outage:** accepted incident remains visible as pending; restarting the worker eventually analyzes it once.
8. **Unmapped source:** event for an unregistered external component is rejected or quarantined with an actionable mapping error, never silently attached.
9. **Resolution and recurrence:** matching resolution closes the incident; a subsequent trigger opens a new one.
10. **Authorization:** disabled integration cannot ingest; a member of another organization cannot list or resolve this incident.

Use integration tests for transaction boundaries, idempotency and tenant isolation; unit tests for normalization, matching and analysis rules. A scripted demo fixture should reproduce the reference timeline without external services.

## 13. Implementation order: get hands dirty

| Step | Build and verify | Done when |
| --- | --- | --- |
| **1. Today: skeleton and vertical cut** | Create Go module, migrations, Docker Compose for PostgreSQL, seed one organization/project/two components/integration, implement `POST /v1/ingest/{id}/events` for `alert.triggered` and `GET /v1/incidents/{id}` | One authenticated curl request creates a persisted incident; another request reads it after process restart |
| **2. Event integrity** | Normalize all four V0 event types; enforce mapping, source ID uniqueness and incident grouping transactionally | Replay, repeat and wrong-component cases pass |
| **3. Timeline** | Add deployment/error intake, incident-event links, sorted timeline endpoint and fixture script | Reference incident displays events in occurrence order despite arrival order |
| **4. Python analysis** | Add durable DB-backed job and Python worker that writes typed findings with evidence IDs | Worker restart/retry and missing-evidence cases pass |
| **5. Engineer view** | Minimal incident list/detail in React + Vite or a simple HTML view; show uncertainty and source links | Engineer can run the full exercise without SQL access |
| **6. Hardening pass** | Cross-tenant and credential tests, bounded inputs, operational metrics and setup docs | V0 acceptance suite passes locally |

The smallest satisfying first coding session is Step 1. It deliberately exercises Go HTTP handling, context, SQL transactions and API design while Python study can continue alongside. Build the Python worker when the incident is already real and queryable, rather than waiting to finish every Python lesson.

## 14. Decisions, assumptions and open questions

**Fixed for V0:** incident investigation is the product; organization/project/component hierarchy; generic authenticated push input; deployment is evidence; one primary component per incident; Go core, PostgreSQL and Python analysis; rule-based findings with provenance; no LLM or broker prerequisite.

**Defaults to validate through the demo:** exact grouping key, 30-minute pre-incident and 15-minute post-incident evidence windows, 15-minute late-event reanalysis window, retention, and whether the first engineer view is React or API-first. These are engineering assumptions, not claims about all customers.

**Later product questions:** Which first native incident provider offers the strongest real-world validation? How do teams name components consistently across tools? When should related alerts across several components form one incident? What evidence is enough to elevate a hypothesis? Which data may leave a customer's telemetry system for AI processing? What is the cost of each investigation? Answer these from realistic incident exercises and user feedback.

## 15. Change control

Proposed additions should name the user problem, their place in the core loop, the simpler option considered, evidence of need and a measurable outcome. If an addition shifts Sentinel toward generic monitoring or automatic remediation, revisit the North Star explicitly. This PRD is detailed enough to build V0, while its provider choices, query recipes and scaling topology remain revisable as evidence appears.
