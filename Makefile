.PHONY: db down run test test-api test-worker test-scripts

TEST_DATABASE_URL ?= postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable

## db: start local PostgreSQL and wait until it is healthy
db:
	docker compose up -d --wait postgres

## down: stop the local stack (keeps the database volume)
down:
	docker compose down

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

## test: run every test suite
test: test-scripts test-api test-worker
