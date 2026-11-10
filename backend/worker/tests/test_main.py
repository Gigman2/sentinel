import threading
import time

from helpers import job_row, reference_incident
from sentinel_worker import main
from sentinel_worker.rules import analyze
from sentinel_worker import store, summary


def test_no_job_returns_false(conn, seeded):
    assert main.process_one(conn) is False


def test_process_reference_incident(conn, seeded):
    incident, events = reference_incident(conn, seeded)
    assert main.process_one(conn) is True

    job_id = conn.execute("SELECT id::text FROM analysis_jobs WHERE incident_id = %s", (incident,)).fetchone()[0]
    assert job_row(conn, job_id)["status"] == "succeeded"
    revision = conn.execute("SELECT id, revision_number FROM analysis_revisions WHERE incident_id = %s",
                            (incident,)).fetchone()
    assert revision[1] == 1

    expected = analyze(store.load_context(conn, incident))
    stored = conn.execute(
        """SELECT f.kind, f.rule_id, f.text, f.limitations,
                  COALESCE(array_agg(fe.event_id::text ORDER BY fe.event_id) FILTER (WHERE fe.event_id IS NOT NULL), '{}')
           FROM findings f LEFT JOIN finding_evidence fe ON fe.finding_id = f.id
           WHERE f.revision_id = %s GROUP BY f.id, f.ordinal ORDER BY f.ordinal""", (revision[0],)).fetchall()
    assert [(k, r, t, lim, sorted(ev)) for k, r, t, lim, ev in stored] == [
        (f.kind, f.rule_id, f.text, f.limitations, sorted(f.evidence_ids)) for f in expected]
    assert any(r == "deployment_near_onset" for _, r, *_ in stored)
    stored_summary = conn.execute("SELECT summary FROM analysis_revisions WHERE id = %s", (revision[0],)).fetchone()[0]
    assert stored_summary == summary.summarize(store.load_context(conn, incident), expected)
    assert stored_summary[-1]["kind"] == "text" and stored_summary[1] == {"text": "14:09", "kind": "time"}
    seen = {r[0] for r in conn.execute("SELECT event_id::text FROM revision_events WHERE revision_id = %s", (revision[0],))}
    assert seen == set(events.values())
    assert main.process_one(conn) is False


def test_steps_progress_in_order(conn, seeded):
    incident, _ = reference_incident(conn, seeded)
    observed = []

    def step_now():
        return conn.execute("SELECT step, step_started_at FROM analysis_jobs WHERE incident_id = %s",
                            (incident,)).fetchone()

    def watching(ctx):
        observed.append(step_now())
        return analyze(ctx)

    assert main.process_one(conn, analyze_fn=watching) is True
    assert observed[0][0] == "draft_findings" and observed[0][1] is not None
    final = step_now()
    assert final[0] == "save_revision" and final[1] >= observed[0][1]


def test_crash_in_analyze_stays_at_draft_findings(conn, seeded):
    incident, _ = reference_incident(conn, seeded)

    def crash(ctx):
        raise RuntimeError("boom")

    main.process_one(conn, analyze_fn=crash)
    row = conn.execute("SELECT status, step FROM analysis_jobs WHERE incident_id = %s", (incident,)).fetchone()
    assert row == ("pending", "draft_findings")  # requeued, still showing where it failed


def test_poison_job_fails_after_max_attempts(conn, seeded):
    incident, _ = reference_incident(conn, seeded)

    def poison(ctx):
        raise RuntimeError("rule exploded")

    for attempt in range(1, 6):
        assert main.process_one(conn, analyze_fn=poison) is True
        row = conn.execute("SELECT status, attempts, last_error FROM analysis_jobs WHERE incident_id = %s",
                           (incident,)).fetchone()
        assert row[1] == attempt and row[2] == "RuntimeError: rule exploded"
        assert row[0] == ("failed" if attempt == 5 else "pending")
        conn.execute("UPDATE analysis_jobs SET next_attempt_at = now()")  # skip the backoff
    assert main.process_one(conn, analyze_fn=poison) is False
    assert conn.execute("SELECT count(*) FROM analysis_revisions").fetchone()[0] == 0


def test_run_stops_on_signal(dsn, conn, seeded):
    incident, _ = reference_incident(conn, seeded)
    stop = threading.Event()
    worker = threading.Thread(target=main.run, args=(dsn,), kwargs={"poll_seconds": 0.05, "stop": stop})
    worker.start()
    try:
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            status = conn.execute("SELECT status FROM analysis_jobs WHERE incident_id = %s", (incident,)).fetchone()[0]
            if status == "succeeded":
                break
            time.sleep(0.05)
        assert status == "succeeded"
    finally:
        stop.set()
        worker.join(timeout=5)
    assert not worker.is_alive()
