# Backend

Everything that runs on the server.

| Folder | What it is |
| --- | --- |
| [`api/`](api/) | Go API: authenticated webhooks, incident grouping, evidence timeline, analysis job queue, engineer API |
| [`worker/`](worker/) | Python worker: leases analysis jobs from PostgreSQL and writes evidence-cited findings |

Both share one PostgreSQL database. The schema migrations live in `api/migrations/` and are the single source of truth for both.
