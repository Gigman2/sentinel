# Sentinel analysis worker (Python)

Runs deterministic, evidence-cited rules over an incident's linked events.
Leasing jobs from PostgreSQL and writing revisions arrive in plan Task 10.

```
worker/
├── sentinel_worker/
│   ├── models.py       EvidenceEvent, IncidentContext, Finding (frozen dataclasses)
│   └── rules.py        analyze(ctx) -> list[Finding]
├── tests/              pytest
├── pyproject.toml      managed with uv (Python ≥ 3.12)
└── uv.lock
```

```bash
cd backend/worker
uv run pytest -q          # or, from the repo root: make test-worker
```

## Rule rules

- Observations and hypotheses cite at least one evidence event; unknowns and next checks may cite none.
- Hypotheses carry a `limitations` sentence. No finding says "root cause", "caused" or "confirmed".
- Empty results are worded "no matching … found", never "healthy".
- Output is deterministic: observations, hypotheses, unknowns, next checks; within a kind, rule order; within a rule, event time order.
- Times are printed as `HH:MM UTC`. The window constants match the API (30 minutes before, 15 after).
