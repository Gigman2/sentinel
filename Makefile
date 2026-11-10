.PHONY: db down up reset run demo showcase test test-api test-worker test-frontend test-scripts

# The database port: the environment, then .env, then 5432 (as Compose does).
SENTINEL_DB_PORT ?= $(or $(shell sed -n 's/^SENTINEL_DB_PORT=//p' .env 2>/dev/null | tail -1),5432)
TEST_DATABASE_URL ?= postgres://sentinel:sentinel@localhost:$(SENTINEL_DB_PORT)/postgres?sslmode=disable

## db: start local PostgreSQL and wait until it is healthy
db:
	docker compose up -d --wait postgres

## up: build and start the whole stack in Docker (UI on http://127.0.0.1:5173, API on :8080)
up: .env
	@./scripts/check-demo-env.sh
	docker compose up -d --build --wait

## down: stop the local stack (keeps the database volume)
down:
	docker compose down

## reset: stop the stack, delete its database volume, and start it again
reset:
	docker compose down -v
	$(MAKE) up

## .env: create .env from .env.example if it does not exist
.env:
	cp -n .env.example .env

## demo: run the demo scenarios against the running stack
demo:
	python3 scripts/demo.py all

## showcase: fill the running stack with varied incidents to explore in the UI
showcase:
	python3 scripts/showcase.py

## run: start PostgreSQL, migrate, seed, then run the API and worker (Ctrl-C stops them)
run:
	./scripts/run-local.sh

## test-api: run the Go API's vet and tests against local PostgreSQL
test-api: db
	cd backend/api && go vet ./... && SENTINEL_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -count=1 ./...

## test-worker: run the Python worker's tests against local PostgreSQL
test-worker: db
	cd backend/worker && SENTINEL_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' uv run pytest -q -W error

## test-scripts: test the shell helpers used by make run
test-scripts:
	./scripts/test-load-env.sh
	./scripts/test-check-demo-env.sh

## test-frontend: type-check, build and test the engineer view
test-frontend:
	cd frontend && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build && npm test

## test: run every test suite
test: test-scripts test-api test-worker test-frontend
