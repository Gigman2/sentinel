# Sentinel — Phase 2: Real Evidence and Distributed Design

**Companion to:** Sentinel PRD v1, Phase 1 guide and six-month curriculum  
**Learning block:** System design (curriculum Months 2–3); AWS foundations begin during Month 3  
**Entry condition:** Phase 1 scripted incident works locally  
**Date:** 8 November 2026

## 1. Outcome

Sentinel moves from a synthetic, same-component timeline to a useful investigation using connected operational systems. One native alert provider can open an incident. Sentinel can query bounded log and metric evidence from Loki and Prometheus, connect a change to the affected component, and notify an engineer. Dependency relationships permit carefully explained cross-component leads. Its processing remains reliable when connectors are slow, duplicated, unavailable or returning incomplete data.

This phase is also the system design laboratory. Capture load assumptions, failure modes and architecture decisions, then change the implementation only where an observed or simulated limitation warrants it. Do not replace the Phase 1 system simply to showcase a broker or microservices.

## 2. Learning questions to answer with the system

| Topic | Concrete question and experiment |
| --- | --- |
| Delivery and idempotency | What happens when a provider retries a webhook and the worker retries the same enrichment job? |
| Backpressure | How many concurrent Loki/Prometheus queries can run before a source or Sentinel becomes unhealthy? |
| Consistency | If an alert arrives before its deployment event, when should an analysis revision be refreshed? |
| Partitioning | If events for many organizations arrive together, how are workers kept fair and tenant-safe? |
| Caching | Does repeating a bounded source query justify caching, and how is stale evidence labeled? |
| Availability | What should the incident page show when one source fails while others succeed? |
| Scale | At 10, 1,000 and 100,000 monitored components, which measured bottleneck appears first? |

Record the setup, measurement and conclusion for each experiment. The purpose is to understand a design tradeoff, not to claim a benchmark from a toy workload.

## 3. Scope and boundaries

### In scope

- One native **incident input** connector, selected after a small integration spike. A Sentry or Alertmanager adapter is a candidate; the choice should be based on access to a realistic demo, documented event semantics and mapping quality.
- Read-only Loki and Prometheus **evidence** adapters using project-scoped configuration and component/environment label mappings.
- Change evidence: keep the generic deployment webhook, and optionally add one native Git/CI connector if the incident exercise exposes a clear gap.
- Component dependency edges and bounded cross-component evidence expansion.
- One outbound **notification** destination with retry and deduplication.
- Load/failure exercises, ADRs and a cloud-ready architecture draft.

### Out of scope

Continuous collection of all telemetry, full replacement for observability tools, automatic root-cause declaration, autonomous remediation, multi-provider parity, an LLM investigator and a mandatory broker migration.

## 4. Integration model

An integration is a configured provider connection with one or more capabilities: incident input, evidence, change evidence or notification. Separate provider adapters from the normalized Sentinel event/evidence contracts. One provider can support more than one capability; a project may connect several providers of the same kind.

Configuration contains organization/project scope, provider, capabilities, connection status, secret reference, environment mapping and explicit external-component or label mapping. A connection test should distinguish authentication failure, unreachable endpoint, missing permission, mapping error, empty result and successful query.

**Mapping example:** Sentinel `payment-service / production` maps to the alert provider's `payments-prod`, Loki selector `app="payments", environment="production"`, and Prometheus selector `service="payments", environment="production"`. The adapter derives tenant scope from its stored connection, not from a query supplied by an incident payload. Log and metric labels are suggestions until an admin confirms the mapping.

## 5. Incident input connector

1. Verify the provider's signature or configured credential and replay protection as applicable.
2. Translate provider-specific alert lifecycle into normalized trigger/update/resolve events. Preserve provider event ID, alert ID, first-seen time, severity, source URL and receipt time.
3. Resolve its external component/environment against configured mapping. If unknown, record a bounded intake diagnostic and do not guess.
4. Apply Phase 1 grouping and idempotency rules before broadening them. Keep the original provider payload only as needed for debugging and redact secrets.
5. Test duplicate delivery, alert flapping, resolve-before-trigger, delayed delivery, signature failure and disabled integration.

If the provider does not offer a stable event ID, define and document an adapter-specific deduplication key. Do not pretend a hash of the entire payload is always stable.

## 6. Bounded evidence queries

### Loki log evidence

For each incident, issue a narrow, read-only range query around the incident onset and affected component(s). Use curated LogQL templates and validated label values; do not interpolate arbitrary user strings into executable queries. Set time-window, result-size, timeout and per-source concurrency budgets. Summarize or select a small number of relevant entries, preserving timestamps, labels, source URL or reproducible query reference and retrieval status. Redact secrets/PII before persisting or displaying snippets.

### Prometheus metric evidence

Use curated PromQL templates for signals such as request error rate, request latency and upstream timeout rate. Define the metric name/labels per integration rather than assuming every deployment uses the same instrumentation. Specify query range, step, timeout and maximum series/sample budget. Persist a compact summary, the expression and window, units, source reference and “no data”/error status. Missing series do not prove health.

### Query orchestration

- Query relevant sources after incident creation and on an authorized refresh. A dependency query is allowed only for a registered relationship and bounded graph depth.
- Store an evidence retrieval attempt with connector, incident, window, query fingerprint, status, timing and count. A partial failure must not erase successful evidence.
- Rate limit and bound concurrent calls by both source and organization. Cancel work when the deadline expires; retry transient failures with bounded backoff and jitter, never unbounded loops.
- Avoid repeated expensive queries from incident-page refreshes. Serve persisted results and offer an explicit refresh with a cooldown or work queue.
- Keep a distinction between raw source fact, normalized evidence, correlation link, hypothesis and human conclusion.

## 7. Correlation and change reasoning

Phase 1 grouped alerts by one primary component. Phase 2 adds an explicit directed edge such as `checkout-api → payment-service` with environment and validity metadata. The investigation may inspect one hop of dependencies and label each linked evidence item with the path: “payment-service is a registered dependency of checkout-api.” This does not merge their alerts into a single incident automatically.

For deployment candidates, capture deployment ID, component, environment, time, version, commit SHA/range and source. Score relevance using component relationship, time proximity, version distribution and supporting logs/metrics. Show **all plausible candidates** when several deployments occurred. A nearby deployment remains a hypothesis until independent evidence differentiates it. A rollback that coincides with recovery strengthens the lead but still requires careful interpretation.

Set a deterministic rule for late evidence and reanalysis revisioning. Reanalysis should preserve the prior conclusion and explain which new evidence changed the findings.

## 8. Notifications

Send incident-created and analysis-ready messages to one configured destination. Include incident ID, project, severity, primary component, concise summary and a link; do not dump raw logs or secrets. Persist a delivery record keyed by incident, event type, destination and revision so retries do not duplicate sends. Delivery is asynchronous; its failure cannot roll back ingestion. Provide retry status and a way for an admin to disable the destination.

## 9. Design experiments and possible evolution

| Experiment | Observe | Possible response |
| --- | --- | --- |
| Burst of webhook events | Database contention, duplicate incident rate, API latency | Tune constraints/indexes, batch work, partition by tenant or component |
| Slow Loki query | Query queue depth and incident readiness | Per-connector concurrency, timeouts, budgeted query plans |
| Worker crash after source query | Duplicate revisions or repeated expensive calls | Idempotent attempt keys, checkpoints and retry boundaries |
| Database unavailable | Accepted vs unaccepted event semantics | Return failure before acknowledgement; client retry and operational alert |
| Many independent consumers | Job scheduling complexity and contention | Evaluate durable broker against DB-backed jobs with explicit cost and delivery semantics |
| Repeated incident-page reads | Source query load | Persist evidence and cache only where safe and measured |

Write ADRs for job transport, connector execution model, data retention, correlation window and cache choice. Include alternatives and the workload evidence. AWS foundation work produces a network, IAM, compute, database, secret and cost sketch for Phase 3; it does not require deploying during this phase.

## 10. Work packages and sequence

| Package | Deliverables | Gate |
| --- | --- | --- |
| **P2-A: provider spike** | Compare two incident providers; implement and test one adapter | Real provider alert and resolution map to correct component |
| **P2-B: source configuration** | Read-only credentials, label mapping, connection test and clear error states | A wrong mapping cannot read or attach unrelated component data |
| **P2-C: evidence adapters** | Loki and Prometheus bounded queries, retrieval attempts and source links | Incident shows selected logs and metric summaries with query provenance |
| **P2-D: dependency and change context** | Component edges, one-hop expansion, deployment candidate ranking | Checkout incident can show payment-service evidence with explicit relationship |
| **P2-E: notification** | One destination, async delivery/retry record and deduplication | One created message and one analysis-ready message despite retries |
| **P2-F: design review** | Failure/load exercises, ADRs, scaling assumptions and cloud-ready design | Decisions trace to an observed need and Phase 3 deploy plan |

## 11. Acceptance and failure scenarios

1. A real provider alert opens one incident and repeated deliveries do not create duplicates.
2. Loki and Prometheus return bounded evidence for the mapped component and window; findings cite their provenance.
3. A checkout incident traverses a registered payment-service dependency, but no unregistered relationship is assumed.
4. Two plausible deployments remain separate candidate changes; Sentinel does not label the latest one “root cause.”
5. Loki failure produces an explicit partial result while Prometheus and deployment evidence remain available.
6. Empty Prometheus data is shown as “no data”; a timeout is shown as an error, not as normal operation.
7. An integration configured for organization A cannot query or attach data to organization B.
8. Concurrent incidents respect connector budgets; a burst does not spawn unbounded source requests.
9. Notification retries do not produce duplicate delivered messages when the provider's idempotency features are available; otherwise Sentinel exposes the remaining at-least-once delivery risk.
10. Restarting worker/connector processing does not lose accepted events or overwrite earlier analysis revisions.

Use a local Loki/Prometheus fixture or controlled test doubles for repeatable integration tests, plus one limited real-provider exercise. Do not load test public providers without authorization.

## 12. Exit package and handoff

Deliver connector configuration instructions, source mappings, query recipes, retrieval/error-state examples, dependency diagram, ADRs, load/failure notes, and a repeatable incident demo. Phase 3 receives a deployment topology, estimated resource requirements, security boundaries, secret inventory and a cost hypothesis. Keep unresolved design questions explicit.
