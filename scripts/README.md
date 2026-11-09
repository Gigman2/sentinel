# Scripts

- `run-local.sh` — run the whole stack locally (`make run`): PostgreSQL in Docker, migrations, the demo tenant seed, the Go API on `127.0.0.1:8080` (set `PORT` to change it) and the Python worker. Ctrl-C stops the API and the worker; `make down` stops PostgreSQL. Needs Docker, Go and uv.

Override `DATABASE_URL`, `HOST`, `PORT` or the `SEED_*` variables in the environment or in a git-ignored `.env` at the repo root; copy `.env.example` to start. The environment wins: `.env` only fills in variables that are not already set, so `PORT=9090 make run` overrides `PORT` in `.env`. The script prints the database it is about to migrate and seed before it starts.

- `load-env.sh` — the `.env` loader used by `run-local.sh` (`load_dotenv FILE`); `test-load-env.sh` tests it (`make test-scripts`). Seed tokens are printed once, when first generated; to keep the same tokens across runs set `SEED_ADMIN_TOKEN`, `SEED_MONITOR_TOKEN` and `SEED_DEPLOY_TOKEN` in `.env`.

The demo scenario driver arrives in plan Task 12.
