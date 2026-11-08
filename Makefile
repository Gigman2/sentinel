.PHONY: db down

## db: start local PostgreSQL and wait until it is healthy
db:
	docker compose up -d --wait postgres

## down: stop the local stack (keeps the database volume)
down:
	docker compose down
