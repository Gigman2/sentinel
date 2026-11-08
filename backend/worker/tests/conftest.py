"""Database fixtures: a fresh, migrated PostgreSQL database per test.

Migrations are read from ../api/migrations (the Go API owns the schema).
Each file's "-- +goose Up" section is executed as one string.
"""

import os
import secrets
from collections.abc import Iterator
from pathlib import Path

import psycopg
import pytest
from psycopg.conninfo import conninfo_to_dict, make_conninfo

MIGRATIONS = Path(__file__).resolve().parents[2] / "api" / "migrations"


def up_section(sql: str) -> str:
    """Return the goose Up section of a migration file."""
    up = sql.split("-- +goose Up", 1)[1]
    return up.split("-- +goose Down", 1)[0]


@pytest.fixture
def dsn() -> Iterator[str]:
    """Create a migrated database and yield its connection string."""
    admin_url = os.environ.get("SENTINEL_TEST_DATABASE_URL")
    if not admin_url:
        pytest.fail("SENTINEL_TEST_DATABASE_URL is not set; start Postgres with `make db` and export "
                    "SENTINEL_TEST_DATABASE_URL=postgres://sentinel:sentinel@localhost:5432/postgres?sslmode=disable")
    name = f"sentinel_test_{secrets.token_hex(6)}"
    with psycopg.connect(admin_url, autocommit=True) as admin:
        admin.execute(f'CREATE DATABASE "{name}"')
    url = make_conninfo(**{**conninfo_to_dict(admin_url), "dbname": name})
    try:
        with psycopg.connect(url, autocommit=True) as conn:
            for path in sorted(MIGRATIONS.glob("*.sql")):
                conn.execute(up_section(path.read_text()))
        yield url
    finally:
        with psycopg.connect(admin_url, autocommit=True) as admin:
            admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')


@pytest.fixture
def conn(dsn: str) -> Iterator[psycopg.Connection]:
    """An autocommit connection to a fresh, migrated database."""
    with psycopg.connect(dsn, autocommit=True) as c:
        yield c


@pytest.fixture
def seeded(conn: psycopg.Connection) -> dict[str, str]:
    """One organization, project, payment-service component, integration and mapping."""
    ids: dict[str, str] = {}
    ids["org"] = conn.execute("INSERT INTO organizations (slug, name) VALUES ('uppsel', 'Uppsel') RETURNING id::text").fetchone()[0]
    ids["project"] = conn.execute(
        "INSERT INTO projects (organization_id, slug, name) VALUES (%s, 'uppsel-production', 'Uppsel') RETURNING id::text",
        (ids["org"],)).fetchone()[0]
    ids["component"] = conn.execute(
        """INSERT INTO components (organization_id, project_id, environment, slug, display_name)
           VALUES (%s, %s, 'production', 'payment-service', 'Payment Service') RETURNING id::text""",
        (ids["org"], ids["project"])).fetchone()[0]
    ids["integration"] = conn.execute(
        """INSERT INTO integrations (organization_id, project_id, name, capability, credential_hash)
           VALUES (%s, %s, 'monitor', 'incident_input', 'test-hash') RETURNING id::text""",
        (ids["org"], ids["project"])).fetchone()[0]
    conn.execute(
        """INSERT INTO integration_component_mappings (integration_id, external_component_id, environment, component_id)
           VALUES (%s, 'payments-prod', 'production', %s)""",
        (ids["integration"], ids["component"]))
    return ids
