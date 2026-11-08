# Sentinel API (Go)

Not built yet. The planned layout:

```
api/
├── cmd/sentinel-api/   entry point: migrate | seed | serve
├── internal/           one package per area (ingest, incidents, analysis, tenancy, …)
├── migrations/         SQL migrations
└── Dockerfile
```
