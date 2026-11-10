from datetime import timedelta

import psycopg
import pytest

from helpers import at, insert_event, job_row, make_incident, reference_incident
from sentinel_worker import store
from sentinel_worker.models import Finding


def expire_lease(conn: psycopg.Connection, job_id: str) -> None:
    conn.execute("UPDATE analysis_jobs SET lease_expires_at = now() - interval '1 second' WHERE id = %s", (job_id,))


def make_simple_incident(conn, seeded, fingerprint="payments-errors") -> str:
    alert = insert_event(conn, seeded, "alert.triggered", at(0), summary="Payment errors", severity="critical",
                         fingerprint=fingerprint)
    return make_incident(conn, seeded, [(alert, "trigger")], fingerprint=fingerprint)


FINDINGS = [
    Finding("observation", "trigger_alert", "An alert fired.", ()),
    Finding("next_check", "inspect_telemetry", "Inspect logs.", ()),
]


def test_lease_job_takes_pending_job(conn, seeded):
    incident = make_simple_incident(conn, seeded)
    job = store.lease_job(conn, lease_seconds=60)

    assert job is not None and job.incident_id == incident and job.attempts == 1 and job.max_attempts == 5
    row = job_row(conn, job.id)
    assert row["status"] == "running" and str(row["lease_token"]) == job.lease_token
    assert store.lease_job(conn) is None  # nothing else to lease


def test_lease_skips_job_not_yet_due(conn, seeded):
    make_simple_incident(conn, seeded)
    conn.execute("UPDATE analysis_jobs SET next_attempt_at = now() + interval '1 minute'")
    assert store.lease_job(conn) is None


def test_expired_lease_is_reclaimed(conn, seeded):
    make_simple_incident(conn, seeded)
    first = store.lease_job(conn)
    expire_lease(conn, first.id)  # the worker holding it died

    second = store.lease_job(conn)
    assert second is not None and second.id == first.id
    assert second.attempts == 2 and second.lease_token != first.lease_token


def test_load_context_reference_incident(conn, seeded):
    incident, events = reference_incident(conn, seeded)
    ctx = store.load_context(conn, incident)

    assert ctx.incident_id == incident and ctx.component_slug == "payment-service" and ctx.environment == "production"
    assert [e.id for e in ctx.events] == [events["deploy"], events["error"], events["alert"]]
    assert [e.link_reason for e in ctx.events] == ["same_component_time_window", "same_component_time_window", "trigger"]
    assert ctx.events[0].details == {"version": "1.8.4"} and ctx.events[0].severity is None
    assert ctx.first_seen_at == at(0) and ctx.events[1].occurred_at == at(-2)


def test_stale_worker_cannot_write(conn, seeded):
    incident = make_simple_incident(conn, seeded)
    stale = store.lease_job(conn)
    expire_lease(conn, stale.id)
    fresh = store.lease_job(conn)

    assert store.write_revision(conn, stale, FINDINGS, ()) is None
    assert conn.execute("SELECT count(*) FROM analysis_revisions").fetchone()[0] == 0
    store.fail_job(conn, stale, RuntimeError("late"))  # also a no-op
    assert job_row(conn, stale.id)["status"] == "running"

    assert store.write_revision(conn, fresh, FINDINGS, ()) is not None
    assert conn.execute("SELECT count(*) FROM analysis_revisions WHERE incident_id = %s", (incident,)).fetchone()[0] == 1


def test_write_revision_idempotent_per_job(conn, seeded):
    alert = insert_event(conn, seeded, "alert.triggered", at(0), summary="x", severity="critical", fingerprint="f")
    make_incident(conn, seeded, [(alert, "trigger")], fingerprint="f")
    job = store.lease_job(conn)
    findings = [Finding("observation", "trigger_alert", "An alert fired.", (alert,)), *FINDINGS[1:]]

    revision = store.write_revision(conn, job, findings, [alert, alert])
    assert revision is not None
    assert store.write_revision(conn, job, findings, [alert, alert]) is None

    row = job_row(conn, job.id)
    assert row["status"] == "succeeded" and row["lease_token"] is None and row["lease_expires_at"] is None
    assert conn.execute("SELECT count(*) FROM analysis_revisions").fetchone()[0] == 1
    rows = conn.execute("SELECT ordinal, kind, rule_id FROM findings WHERE revision_id = %s ORDER BY ordinal",
                        (revision,)).fetchall()
    assert rows == [(0, "observation", "trigger_alert"), (1, "next_check", "inspect_telemetry")]
    assert conn.execute("SELECT count(*) FROM finding_evidence").fetchone()[0] == 1
    # The events the analysis read, once each; the replay wrote nothing more.
    assert conn.execute("SELECT revision_id::text, event_id::text FROM revision_events").fetchall() == [(revision, alert)]


def test_second_revision_increments(conn, seeded):
    incident = make_simple_incident(conn, seeded)
    store.write_revision(conn, store.lease_job(conn), FINDINGS, ())
    conn.execute("INSERT INTO analysis_jobs (incident_id, reason) VALUES (%s, 'late_event')", (incident,))
    store.write_revision(conn, store.lease_job(conn), FINDINGS, ())

    numbers = [r[0] for r in conn.execute(
        "SELECT revision_number FROM analysis_revisions WHERE incident_id = %s ORDER BY revision_number", (incident,))]
    assert numbers == [1, 2]


def test_failure_requeues_with_backoff(conn, seeded):
    make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    store.fail_job(conn, job, RuntimeError("boom\nsecond line"))

    row = job_row(conn, job.id)
    assert row["status"] == "pending" and row["lease_token"] is None and row["last_error"] == "RuntimeError: boom"
    delay = conn.execute("SELECT next_attempt_at - now() FROM analysis_jobs WHERE id = %s", (job.id,)).fetchone()[0]
    assert timedelta(seconds=8) < delay <= timedelta(seconds=10)  # 5s × 2^1


def test_last_error_truncated():
    message = store.error_message(ValueError("x" * 1000))
    assert len(message) == 500 and message.startswith("ValueError: xxx")


def test_crashing_worker_job_fails_after_max_attempts(conn, seeded):
    make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    conn.execute("UPDATE analysis_jobs SET attempts = max_attempts WHERE id = %s", (job.id,))
    expire_lease(conn, job.id)  # the worker died on its last attempt

    assert store.lease_job(conn) is None
    row = job_row(conn, job.id)
    assert row["status"] == "failed" and row["last_error"] == "lease expired after max attempts"
    assert row["lease_token"] is None


def test_retry_superseded_by_newer_pending_job(conn, seeded):
    incident = make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    conn.execute("INSERT INTO analysis_jobs (incident_id, reason) VALUES (%s, 'late_event')", (incident,))

    store.fail_job(conn, job, RuntimeError("boom"))
    assert job_row(conn, job.id)["status"] == "failed"
    assert job_row(conn, job.id)["last_error"] == "superseded by a newer pending analysis job"
    assert conn.execute("SELECT count(*) FROM analysis_jobs WHERE status = 'pending'").fetchone()[0] == 1


def test_retry_unique_violation_falls_back_to_superseded(conn, seeded):
    incident = make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    conn.execute("INSERT INTO analysis_jobs (incident_id, reason) VALUES (%s, 'late_event')", (incident,))

    # Skip the pending check so the requeue itself hits uq_jobs_one_pending.
    store._requeue(conn, job, "RuntimeError: boom", check_pending=False)
    row = job_row(conn, job.id)
    assert row["status"] == "failed" and row["last_error"] == "superseded by a newer pending analysis job"


def test_max_attempts_with_pending_job_keeps_real_error(conn, seeded):
    incident = make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    conn.execute("UPDATE analysis_jobs SET attempts = max_attempts WHERE id = %s", (job.id,))
    conn.execute("INSERT INTO analysis_jobs (incident_id, reason) VALUES (%s, 'late_event')", (incident,))
    job = store.Job(job.id, job.incident_id, job.max_attempts, job.max_attempts, job.lease_token)

    store.fail_job(conn, job, RuntimeError("boom"))
    row = job_row(conn, job.id)
    assert row["status"] == "failed" and row["last_error"] == "RuntimeError: boom"


@pytest.mark.parametrize("attempts, seconds", [(1, 10), (3, 40), (6, 300), (10, 300)])
def test_backoff_capped(attempts, seconds):
    assert store.backoff_seconds(attempts) == seconds


def test_lease_skips_locked_job(dsn, conn, seeded):
    first = make_simple_incident(conn, seeded, fingerprint="first")
    second = make_simple_incident(conn, seeded, fingerprint="second")
    with psycopg.connect(dsn) as other:  # another worker mid-lease on the first job
        other.execute("SELECT id FROM analysis_jobs WHERE incident_id = %s FOR UPDATE", (first,))
        conn.execute("SET lock_timeout = '2s'")  # without SKIP LOCKED this would wait, then error
        job = store.lease_job(conn)
        assert job is not None and job.incident_id == second


def test_started_and_finished_at(conn, seeded):
    """started_at is the first lease and survives a re-lease and a requeue;
    finished_at is set when the job succeeds or fails, never before."""
    make_simple_incident(conn, seeded)
    first = store.lease_job(conn)
    started = job_row(conn, first.id)["started_at"]
    assert started is not None and job_row(conn, first.id)["finished_at"] is None

    conn.execute("UPDATE analysis_jobs SET started_at = started_at - interval '1 hour' WHERE id = %s", (first.id,))
    started = job_row(conn, first.id)["started_at"]
    expire_lease(conn, first.id)
    second = store.lease_job(conn)
    assert second.id == first.id and job_row(conn, first.id)["started_at"] == started

    store.fail_job(conn, second, RuntimeError("boom"))  # requeued
    conn.execute("UPDATE analysis_jobs SET next_attempt_at = now() WHERE id = %s", (first.id,))
    third = store.lease_job(conn)
    row = job_row(conn, third.id)
    assert row["started_at"] == started and row["finished_at"] is None

    store.write_revision(conn, third, [Finding("unknown", "no_error_evidence", "None.", ())], ())
    row = job_row(conn, third.id)
    assert row["status"] == "succeeded" and row["finished_at"] is not None and row["finished_at"] >= started


def test_failed_job_records_finished_at(conn, seeded):
    make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    conn.execute("UPDATE analysis_jobs SET attempts = max_attempts WHERE id = %s", (job.id,))
    store.fail_job(conn, store.Job(job.id, job.incident_id, job.max_attempts, job.max_attempts, job.lease_token),
                   RuntimeError("boom"))
    row = job_row(conn, job.id)
    assert row["status"] == "failed" and row["finished_at"] is not None


def test_expired_last_attempt_records_finished_at(conn, seeded):
    make_simple_incident(conn, seeded)
    job = store.lease_job(conn)
    conn.execute("UPDATE analysis_jobs SET attempts = max_attempts WHERE id = %s", (job.id,))
    expire_lease(conn, job.id)
    assert store.lease_job(conn) is None
    row = job_row(conn, job.id)
    assert row["status"] == "failed" and row["finished_at"] is not None


def test_set_step_is_lease_guarded(conn, seeded):
    make_simple_incident(conn, seeded)
    stale = store.lease_job(conn)
    assert store.set_step(conn, stale, "collect_evidence") is True
    expire_lease(conn, stale.id)
    fresh = store.lease_job(conn)
    assert store.set_step(conn, stale, "draft_findings") is False
    assert job_row(conn, stale.id)["step"] == "collect_evidence"
    assert store.set_step(conn, fresh, "draft_findings") is True
    with pytest.raises(ValueError):
        store.set_step(conn, fresh, "nap")
