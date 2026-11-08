# Scripts

- `run-local.sh` — run the whole stack locally (`make run`): PostgreSQL in Docker, migrations, the demo tenant seed, the Go API on `127.0.0.1:8080` and the Python worker. Ctrl-C stops the API and the worker; `make down` stops PostgreSQL. Needs Docker, Go and uv.

Override `DATABASE_URL`, `ADDR` or the `SEED_*` variables in the environment or in a git-ignored `.env` at the repo root. Seed tokens are printed once, when first generated; to keep the same tokens across runs set `SEED_ADMIN_TOKEN`, `SEED_MONITOR_TOKEN` and `SEED_DEPLOY_TOKEN` in `.env`.

The demo scenario driver arrives in plan Task 12.
