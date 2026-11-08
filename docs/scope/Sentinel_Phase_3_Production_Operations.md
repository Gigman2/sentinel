# Sentinel — Phase 3: Production Operations

**Companion to:** Sentinel PRD v1 and Phases 1–2 guides  
**Learning block:** AWS foundation in curriculum Month 3; deployment, DevOps and Solutions Architect preparation in Month 4  
**Entry condition:** Phase 2 incident and evidence flow works locally  
**Date:** 8 November 2026

## 1. Outcome

Deploy the existing Sentinel product into a controlled AWS environment with reproducible infrastructure, CI/CD, least-privilege access, protected secrets, observability, backups and a tested recovery path. An alert should travel from webhook through incident persistence, evidence collection and Python analysis without an operator manually starting processes. A failed worker should recover without losing accepted work.

The deployment is a learning environment and portfolio artifact. It should be cost-conscious and safe for test data. Do not invite real customer telemetry until retention, access, privacy and connector security are adequately reviewed.

## 2. Architecture decisions before provisioning

Produce a short decision record for each of these rather than treating a service name as a requirement:

| Decision | First candidate | Alternative and deciding evidence |
| --- | --- | --- |
| Compute | Managed container service for Go API and Python worker | VM for lower complexity/cost at tiny scale; Kubernetes only if orchestration needs justify it |
| Database | Managed PostgreSQL | Self-managed database only with an explicit operational rationale |
| Job transport | Keep Phase 1/2 database job if healthy | Managed queue if job contention, fan-out or isolation warrants migration |
| Secret storage | Managed secret service or encrypted environment injection | Never embed tokens in image, Terraform state output or repository |
| Network exposure | Public ingress only for webhook/API surfaces | Worker, database and internal connector traffic stay private |
| Frontend | Static build behind managed hosting/CDN or small service | Choose on deployment simplicity and budget |

Document monthly cost assumptions, expected load, service quotas, availability requirements and the data region. The curriculum's AWS concepts are applied to this actual design: VPC/subnets/routing, IAM, compute, load balancing, managed database, secrets, logging, backup and recovery.

## 3. Workstream A — Reproducible build and environments

- Keep local Docker Compose for development, including migrations and fixture data.
- Produce versioned container images for Go API and Python worker. Use pinned dependency files and a non-root runtime where practical.
- Define a configuration contract: database URL, service ports, integration secret references, job lease settings, query budgets and log settings. Validate required configuration at startup.
- Provision a separate test/staging environment before any production-like one. Use synthetic data in staging; never point fixtures at a live customer's systems.
- Express infrastructure in Terraform modules or a small transparent root configuration. Review plan output before apply; separate state and secrets from source code.
- Document initial setup, environment teardown, migration path and cost controls. Avoid a cluster merely to meet the curriculum.

**Gate:** a new environment can be provisioned from documented inputs, initialized with migrations, and removed without manually recreating hidden console settings.

## 4. Workstream B — CI/CD and release safety

Build a GitHub Actions pipeline that runs Go and Python tests, migration checks, static checks as useful, container build and image scan. On an approved branch/environment, deploy an immutable image version. Separate build and deploy permissions. Use short-lived cloud credentials where supported and restrict the workflow's permissions.

Migration policy must account for rolling deployment: prefer backward-compatible schema additions, deploy application support, migrate data, then remove obsolete fields later. Record deployed image and migration version. Provide a rollback procedure that does not assume a database migration can always be reversed.

**Gate:** a small application change runs tests, builds, deploys to staging, is observable, and can be rolled back to the previous image while preserving accepted incident data.

## 5. Workstream C — Identity, network and secrets

- Only the public webhook/API ingress needs exposure. Database and worker have no public inbound route. Egress to configured evidence providers is controlled and logged where practical.
- Give API, worker, migration and CI deployment roles separate minimum permissions. Avoid one broadly privileged credential for all services.
- Store integration credentials and upstream read tokens in a secret store or encrypted field with managed key policy. Rotate and revoke them without rebuilding images.
- Preserve organization/project authorization in the API and connector worker. A network boundary does not replace tenant checks.
- Define how remote Loki/Prometheus endpoints are reached. Private endpoints may require VPN, private connectivity or an agent/proxy architecture; do not silently expose a private observability service to the internet.
- Treat configured connector URLs as SSRF-sensitive. Limit destinations and redirects; later AppSec review deepens this control.

**Gate:** credentials do not appear in logs, image layers, CI output or Terraform outputs; a disabled connector cannot query its provider.

## 6. Workstream D — Observability of Sentinel

Correlate a request ID, event ID, incident ID, analysis job ID and evidence retrieval attempt across structured logs. Instrument counters/rates for accepted/rejected/duplicate events, open incidents and notification attempts. Instrument duration and queue lag for analysis jobs and connector queries. Record retry count, timeout and terminal failure states.

Trace the path from ingest request to persisted event, incident match, job scheduling, worker execution, evidence query and revision write. Async trace context may require explicit propagation through the durable job. Do not put credentials or raw sensitive log bodies in spans.

Set a small number of operational alerts: high intake rejection rate, analysis backlog age, worker dead-letter/terminal failures, connector failure rate, database availability and abnormal spend. Document where an operator begins when Sentinel itself is unhealthy.

**Gate:** during a synthetic incident, an engineer can find where processing stopped and distinguish “no provider evidence” from “provider query failed.”

## 7. Workstream E — Reliability and recovery

- Define acknowledgement semantics: Sentinel returns success only after the incoming event and required durable handoff commit. Rejected/uncommitted events can be retried by the sender.
- Ensure job lease expiry and idempotent revision writes recover from worker death after a query but before result persistence.
- Use bounded retries/backoff; terminal jobs remain inspectable and manually retryable. Set connector timeouts independently of the API request deadline.
- Back up PostgreSQL and test restore into a separate environment. Record recovery time and data-loss observations from the exercise rather than promising an unmeasured RTO/RPO.
- Have a deployment rollback, database migration compatibility and service restart playbook. Include how to disable a faulty integration without deleting its evidence.
- Define a retention and deletion policy before storing anything beyond synthetic test data.

**Gate:** kill the worker during analysis, restart it, verify eventual single coherent revision; restore a backup and verify incident and evidence records.

## 8. Sequence and deliverables

| Package | Build | Evidence of completion |
| --- | --- | --- |
| **P3-A: architecture and budget** | AWS topology, ADRs, cost estimate and data boundary | Reviewed deployment diagram with explicit assumptions |
| **P3-B: containers and IaC** | Images, Terraform, database, compute, network and secret references | Reproduce staging from clean state |
| **P3-C: delivery pipeline** | Tests, build, image scan, migration and deployment jobs | One change deploys and rolls back safely |
| **P3-D: telemetry and alerts** | Logs, metrics, tracing, dashboards and operator alerts | Trace one incident end to end and diagnose injected failure |
| **P3-E: recovery drill** | Backup/restore, worker-failure test and runbooks | Evidence of recovery and documented limitations |
| **P3-F: cost and security review** | Budget guardrails, IAM review and credential rotation exercise | No broad shared credential or unexplained public endpoint |

The AWS certification target in the curriculum is a learning objective, not a deployment acceptance criterion. Sentinel should keep working whether the exam happens earlier or later.

## 9. Acceptance scenarios

1. A configured staging webhook creates an incident and Python analysis without manual process intervention.
2. A duplicate webhook after deployment remains idempotent; another organization's token cannot access the incident.
3. A connector timeout produces a partial incident with a visible retrieval failure while other evidence survives.
4. Worker termination mid-job eventually recovers; no accepted event disappears and no duplicate final revision appears.
5. A code change deploys via CI/CD and a rollback restores the prior image without data loss.
6. A backup restores into a separate environment and preserves a sample incident's event links and analysis revisions.
7. The on-call runbook identifies job backlog, connector failure, database outage and credential rotation steps.
8. The cost report compares estimate with observed spend for a bounded test period and flags unexpected resources.

## 10. Exit package and handoff

Deliver architecture diagram, Terraform, pipeline configuration, migration strategy, staging URL and fixture instructions, dashboard/alert definitions, trace example, IAM/secret inventory, cost analysis, backup/restore evidence and runbooks. Record known operational and privacy limits.

Phase 4 inherits a reliable evidence pipeline and an evaluation environment. AI requests must use the same authorization, provenance, logging and cost controls; they do not receive a privileged bypass around existing tenant boundaries.
