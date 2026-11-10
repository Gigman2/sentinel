"""Row builders for worker tests. Imported normally, not via conftest."""

import itertools
from datetime import UTC, datetime, timedelta
from typing import Any

import psycopg
from psycopg.types.json import Jsonb

FIRST_SEEN = datetime(2026, 11, 8, 14, 9, tzinfo=UTC)
_source_ids = itertools.count(1)


def at(minutes: float) -> datetime:
    return FIRST_SEEN + timedelta(minutes=minutes)


def insert_event(conn: psycopg.Connection, seeded: dict[str, str], type: str, occurred_at: datetime, *,
                 summary: str = "", severity: str | None = None, fingerprint: str | None = None,
                 details: dict[str, Any] | None = None, attributes: dict[str, Any] | None = None) -> str:
    """Insert an operational event for payment-service and return its ID."""
    return conn.execute(
        """INSERT INTO operational_events (organization_id, project_id, integration_id, component_id, environment,
                                           type, source_event_id, occurred_at, received_at, severity, fingerprint,
                                           summary, attributes, details, disposition)
           VALUES (%s, %s, %s, %s, 'production', %s, %s, %s, %s, %s, %s, %s, %s, %s, 'evidence')
           RETURNING id::text""",
        (seeded["org"], seeded["project"], seeded["integration"], seeded["component"], type,
         f"src-{next(_source_ids)}", occurred_at, occurred_at, severity, fingerprint, summary,
         Jsonb(attributes or {}), Jsonb(details or {}))).fetchone()[0]


def make_incident(conn: psycopg.Connection, seeded: dict[str, str], links: list[tuple[str, str]], *,
                  first_seen: datetime = FIRST_SEEN, last_seen: datetime | None = None,
                  fingerprint: str = "payments-errors") -> str:
    """Create an open incident whose trigger is the first ("trigger") link,
    link the given (event_id, reason) pairs, enqueue a pending job and
    return the incident ID."""
    trigger_id = next(event_id for event_id, reason in links if reason == "trigger")
    incident_id = conn.execute(
        """WITH counter AS (
               UPDATE projects SET next_incident_number = next_incident_number + 1
               WHERE id = %(project)s RETURNING next_incident_number - 1 AS number)
           INSERT INTO incidents (organization_id, project_id, component_id, environment, fingerprint, status,
                                  severity, title, first_seen_at, last_seen_at, trigger_event_id, number)
           SELECT %(org)s, %(project)s, %(component)s, 'production', %(fingerprint)s, 'open', 'critical',
                  'Payment errors affecting checkout', %(first)s, %(last)s, %(trigger)s, counter.number
           FROM counter
           RETURNING id::text""",
        {"org": seeded["org"], "project": seeded["project"], "component": seeded["component"],
         "fingerprint": fingerprint, "first": first_seen, "last": last_seen or first_seen,
         "trigger": trigger_id}).fetchone()[0]
    for event_id, reason in links:
        conn.execute("INSERT INTO incident_events (incident_id, event_id, reason, linked_at) VALUES (%s, %s, %s, now())",
                     (incident_id, event_id, reason))
    conn.execute("INSERT INTO analysis_jobs (incident_id, reason) VALUES (%s, 'incident_created')", (incident_id,))
    return incident_id


def reference_incident(conn: psycopg.Connection, seeded: dict[str, str]) -> tuple[str, dict[str, str]]:
    """The PRD reference incident: deployment at −7m, error at −2m, alert at 0."""
    events = {
        "deploy": insert_event(conn, seeded, "deployment.completed", at(-7), summary="Deploy payment-service 1.8.4",
                               details={"version": "1.8.4"}),
        "error": insert_event(conn, seeded, "error.observed", at(-2), summary="PaymentGatewayTimeout",
                              severity="error", attributes={"count": 42}),
        "alert": insert_event(conn, seeded, "alert.triggered", at(0), summary="Payment errors affecting checkout",
                              severity="critical", fingerprint="payments-errors"),
    }
    incident = make_incident(conn, seeded, [(events["alert"], "trigger"), (events["deploy"], "same_component_time_window"),
                                            (events["error"], "same_component_time_window")])
    return incident, events


def job_row(conn: psycopg.Connection, job_id: str) -> dict[str, Any]:
    cur = conn.execute("SELECT * FROM analysis_jobs WHERE id = %s", (job_id,))
    names = [d.name for d in cur.description]
    return dict(zip(names, cur.fetchone(), strict=True))
