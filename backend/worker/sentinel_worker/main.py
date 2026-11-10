"""The worker process: lease a job, analyse the incident, write a revision.

Run with: DATABASE_URL=postgres://… python -m sentinel_worker.main
"""

import json
import os
import signal
import sys
import threading
from collections.abc import Callable
from datetime import UTC, datetime

import psycopg

from sentinel_worker import rules, store, summary
from sentinel_worker.models import Finding, IncidentContext

AnalyzeFn = Callable[[IncidentContext], list[Finding]]


def log(**fields: object) -> None:
    """One JSON line per event. Never event attributes or details."""
    print(json.dumps({"ts": datetime.now(UTC).isoformat(), **fields}), file=sys.stderr, flush=True)


def process_one(conn: psycopg.Connection, analyze_fn: AnalyzeFn = rules.analyze) -> bool:
    """Lease and process one job. Returns False if no job was due.

    A failure in loading or analysing is recorded on the job (retry, or
    failed after max attempts). A lost database connection is re-raised: the
    lease expires and another attempt reclaims the job.
    """
    job = store.lease_job(conn)
    if job is None:
        return False
    fields = {"job_id": job.id, "incident_id": job.incident_id, "attempts": job.attempts}
    try:
        # Each step is recorded before it runs (the lease already set
        # collect_evidence), so a failure leaves the job at the step that
        # failed. A lost lease stops the job here.
        revision = None
        context = store.load_context(conn, job.incident_id)
        if store.set_step(conn, job, "draft_findings"):
            findings = analyze_fn(context)
            segments = summary.summarize(context, findings)
            if store.set_step(conn, job, "save_revision"):
                revision = store.write_revision(conn, job, findings, [e.id for e in context.events], segments)
    except psycopg.OperationalError:
        raise
    except Exception as error:  # noqa: BLE001 — any rule or data error is recorded on the job
        store.fail_job(conn, job, error)
        outcome = "failed" if job.attempts >= job.max_attempts else "retry"
        log(**fields, outcome=outcome, error=type(error).__name__)
        return True
    log(**fields, outcome="succeeded" if revision else "lease_lost")
    return True


def run(dsn: str, poll_seconds: float = 2.0, stop: threading.Event | None = None) -> None:
    """Process jobs until stopped (SIGTERM/SIGINT, or `stop` being set).

    The job in flight is always finished before exiting. After a lost
    connection the worker waits poll_seconds and reconnects.
    """
    if stop is None:
        stop = threading.Event()
        for sig in (signal.SIGTERM, signal.SIGINT):
            signal.signal(sig, lambda *_: stop.set())

    log(outcome="started")
    conn: psycopg.Connection | None = None
    while not stop.is_set():
        try:
            if conn is None or conn.closed:
                conn = psycopg.connect(dsn, autocommit=True)
            if not process_one(conn):
                stop.wait(poll_seconds)
        except psycopg.OperationalError as error:
            log(outcome="database_unavailable", error=type(error).__name__)
            if conn is not None:
                conn.close()
            conn = None
            stop.wait(poll_seconds)
    if conn is not None:
        conn.close()
    log(outcome="stopped")


def main() -> None:
    dsn = os.environ.get("DATABASE_URL")
    if not dsn:
        sys.exit("DATABASE_URL is required")
    run(dsn, poll_seconds=float(os.environ.get("POLL_SECONDS", "2")))


if __name__ == "__main__":
    main()
