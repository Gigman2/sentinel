# Sentinel analysis worker (Python)

Leases analysis jobs from PostgreSQL, runs deterministic, evidence-cited
rules over the incident's linked events and writes an immutable revision.

```
worker/
├── sentinel_worker/
│   ├── models.py       EvidenceEvent, IncidentContext, Finding (frozen dataclasses)
│   ├── rules.py        analyze(ctx) -> list[Finding]
│   ├── store.py        lease_job, load_context, write_revision, fail_job
│   └── main.py         process_one, run (the process entry point)
├── tests/              pytest; DB tests get a fresh database migrated from ../api/migrations
├── pyproject.toml      managed with uv (Python ≥ 3.12)
└── uv.lock
```

```bash
make db                                   # from the repo root
export SENTINEL_TEST_DATABASE_URL='postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable'
cd backend/worker
uv run pytest -q -W error                 # or, from the repo root: make test-worker

DATABASE_URL=postgres://… uv run python -m sentinel_worker.main   # run the worker
```

## Job lifecycle

- `pending` → leased (`running`, `attempts + 1`, a lease token valid 60 s) → `succeeded` with a new revision.
- A failure requeues with backoff `min(5 s × 2^attempts, 300 s)`, or fails once `attempts` reaches `max_attempts` (5).
- If newer work is already pending for the incident, a failed attempt is marked superseded instead of requeued.
- A worker that dies leaves its lease to expire; the next lease reclaims the job. On its last attempt the job is failed instead (`lease expired after max attempts`).
- Every write to a leased job checks its lease token, so a worker whose lease was taken over cannot change anything.
- SIGTERM/SIGINT finish the job in flight, then exit. Logs are JSON lines with job and incident IDs only.

## Rule rules

- Observations and hypotheses cite at least one evidence event; unknowns and next checks may cite none.
- Hypotheses carry a `limitations` sentence. No finding says "root cause", "caused" or "confirmed".
- Empty results are worded "no matching … found", never "healthy".
- Output is deterministic: observations, hypotheses, unknowns, next checks; within a kind, rule order; within a rule, event time order.
- Times are printed as `HH:MM UTC`. The window constants match the API (30 minutes before, 15 after).
