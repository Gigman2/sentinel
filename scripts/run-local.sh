#!/usr/bin/env bash
# Run Sentinel locally: PostgreSQL (Docker), the Go API and the Python worker.
# Ctrl-C stops the API and the worker; PostgreSQL keeps running (`make down`).
#
# Settings come from the environment, then from a git-ignored .env at the
# repo root if present (see .env.example). To keep the same tokens across runs, set
# SEED_ADMIN_TOKEN, SEED_MONITOR_TOKEN and SEED_DEPLOY_TOKEN there; otherwise
# tokens are generated and printed once, on the first run against a fresh
# database.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

export DATABASE_URL="${DATABASE_URL:-postgres://sentinel:sentinel@localhost:5432/sentinel?sslmode=disable}"
export HOST="${HOST:-127.0.0.1}"
export PORT="${PORT:-8080}"

echo "==> starting PostgreSQL"
docker compose up -d --wait postgres

echo "==> building the API"
(cd backend/api && go build -o bin/sentinel-api ./cmd/sentinel-api)
api_bin="$root/backend/api/bin/sentinel-api"

echo "==> migrating and seeding"
"$api_bin" migrate
"$api_bin" seed

echo "==> syncing worker dependencies"
(cd backend/worker && uv sync --quiet)

pids=()
cleanup() {
  trap - INT TERM EXIT
  echo
  echo "==> stopping API and worker"
  kill -TERM "${pids[@]}" 2>/dev/null || true
  wait "${pids[@]}" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"$api_bin" serve &
pids+=($!)
(cd backend/worker && exec uv run --quiet python -m sentinel_worker.main) &
pids+=($!)

echo "==> API on http://$HOST:$PORT, worker running. Ctrl-C to stop."

# Stop everything if either process exits on its own.
while kill -0 "${pids[0]}" 2>/dev/null && kill -0 "${pids[1]}" 2>/dev/null; do
  sleep 1
done
echo "==> a process exited; shutting down"
