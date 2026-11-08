# Sentinel

Sentinel is an incident investigation platform for engineering teams. An alert starts an investigation. Sentinel then finds the affected component, gathers the related evidence, puts it in time order, and suggests careful next checks. Its goal is to shorten the engineer's **time to understanding**.

The core loop is **Ingest → Correlate → Investigate → Explain**. See [`docs/scope/Sentinel_PRD.md`](docs/scope/Sentinel_PRD.md) for the full product requirements.

## Repository layout

```
sentinel/
├── backend/
│   ├── api/            Go API: ingestion, incidents, evidence, analysis jobs
│   └── worker/         Python analysis worker
├── frontend/           Engineer view (React + Vite)
├── docs/
│   ├── scope/          PRD and phase scope documents
│   ├── adr/            Architecture decision records
│   └── plans/          Implementation plans
├── scripts/            Demo and fixture scripts
├── .github/workflows/  CI
├── docker-compose.yml  Local stack
└── Makefile            Common tasks
```

Everything that runs on the server lives under `backend/`. Each component's README covers its own setup.

## Prerequisites

- Docker with Compose 2.24 or later
- Go 1.26+ (for `backend/api`)
- Python 3.12+ and [uv](https://docs.astral.sh/uv/) (for `backend/worker`)
- Node 22+ (for `frontend`)

## Getting started

```sh
make db      # start local PostgreSQL on 127.0.0.1:5432
make down    # stop the local stack
```

More targets arrive as each component is built.

## How work lands

Each unit of work is a small pull request against `main`. CI runs on every PR, and nothing merges until it has been reviewed. Implementation plans live in `docs/plans/`, and decisions that change defaults are recorded in `docs/adr/`.
