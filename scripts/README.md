# Scripts

- `run-local.sh` — run the whole stack locally (`make run`): PostgreSQL in Docker, migrations, the demo tenant seed, the Go API on `127.0.0.1:8080` (set `PORT` to change it) and the Python worker. Ctrl-C stops the API and the worker; `make down` stops PostgreSQL. Needs Docker, Go and uv.

Override `DATABASE_URL`, `HOST`, `PORT` or the `SEED_*` variables in the environment or in a git-ignored `.env` at the repo root; copy `.env.example` to start. The environment wins: `.env` only fills in variables that are not already set, so `PORT=9090 make run` overrides `PORT` in `.env`. The script prints the database it is about to migrate and seed before it starts.

- `load-env.sh` — the `.env` loader used by `run-local.sh` (`load_dotenv FILE`); `test-load-env.sh` tests it (`make test-scripts`). Seed tokens are printed once, when first generated; to keep the same tokens across runs set `SEED_ADMIN_TOKEN`, `SEED_MONITOR_TOKEN` and `SEED_DEPLOY_TOKEN` in `.env`.

- `demo.py` — the demo scenario driver (`make demo`): `happy`, `missing-deployment`, `late-deployment`, `duplicate` or `all`. Standard library only. Each run registers its own `demo-<id>` component. Reads the API address (`SENTINEL_URL`, else `SENTINEL_API_PORT`, else `PORT`) and the `SEED_*` tokens from the environment or `.env`.
- `showcase.py` — fills the running stack with varied incidents to explore in the UI (`make showcase`); rerunning it changes nothing. Same settings as `demo.py`.
- `check-demo-env.sh` — run by `make up`: stops with the lines to add when `.env` lacks the `SEED_*` tokens; `test-check-demo-env.sh` tests it.
