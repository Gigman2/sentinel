.PHONY: db down test test-api test-worker

TEST_DATABASE_URL ?= postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable

## db: start local PostgreSQL and wait until it is healthy
db:
	docker compose up -d --wait postgres

## down: stop the local stack (keeps the database volume)
down:
	docker compose down

## test-api: run the Go API's vet and tests against local PostgreSQL
test-api: db
	cd backend/api && go vet ./... && SENTINEL_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -count=1 ./...

## test-worker: run the Python worker's tests
test-worker:
	cd backend/worker && SENTINEL_TEST_DATABASE_URL='$(TEST_DATABASE_URL)' uv run pytest -q -W error

## test: run every test suite
test: test-api test-worker
