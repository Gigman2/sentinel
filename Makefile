.PHONY: db down test-api

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
