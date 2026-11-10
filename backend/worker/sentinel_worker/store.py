"""PostgreSQL access for the worker: leasing jobs, loading an incident's
evidence, writing revisions and recording failures.

Every function takes an autocommit connection and opens its own transaction
where it needs one. Writes to a leased job are guarded by its lease token,
so a worker whose lease expired (and was taken over) cannot change anything.

Lock order matches the API: incident row lock, then job rows. The worker
never takes the per-component advisory lock; it does not change which events
are linked.
"""

from collections.abc import Sequence
from dataclasses import dataclass

import psycopg
from psycopg import errors

from psycopg.types.json import Jsonb

from sentinel_worker.models import EvidenceEvent, Finding, IncidentContext
from sentinel_worker.summary import Segment

MAX_ERROR_CHARS = 500
BACKOFF_BASE_SECONDS = 5
BACKOFF_CAP_SECONDS = 300
SUPERSEDED = "superseded by a newer pending analysis job"
LEASE_EXPIRED = "lease expired after max attempts"


class _LostLease(Exception):
    """Raised inside a transaction to roll it back when the lease is gone."""


@dataclass(frozen=True)
class Job:
    id: str
    incident_id: str
    attempts: int
    max_attempts: int
    lease_token: str


def lease_job(conn: psycopg.Connection, lease_seconds: int = 60) -> Job | None:
    """Lease the next due job, or return None.

    In one transaction: first fail running jobs whose lease expired on their
    last attempt (their worker died every time), then take one pending job
    that is due, or a running job whose lease expired with attempts left.
    started_at records the first lease and is kept on later ones.
    """
    with conn.transaction():
        conn.execute(
            """UPDATE analysis_jobs
               SET status = 'failed', last_error = %s, lease_token = NULL, lease_expires_at = NULL,
                   finished_at = now(), updated_at = now()
               WHERE status = 'running' AND lease_expires_at < now() AND attempts >= max_attempts""",
            (LEASE_EXPIRED,))
        row = conn.execute(
            """UPDATE analysis_jobs
               SET status = 'running', attempts = attempts + 1, lease_token = gen_random_uuid(),
                   lease_expires_at = now() + make_interval(secs => %s),
                   started_at = COALESCE(started_at, now()), updated_at = now()
               WHERE id = (
                   SELECT id FROM analysis_jobs
                   WHERE (status = 'pending' AND next_attempt_at <= now())
                      OR (status = 'running' AND lease_expires_at < now() AND attempts < max_attempts)
                   ORDER BY next_attempt_at, created_at
                   FOR UPDATE SKIP LOCKED
                   LIMIT 1)
               RETURNING id::text, incident_id::text, attempts, max_attempts, lease_token::text""",
            (lease_seconds,)).fetchone()
    return Job(*row) if row else None


def load_context(conn: psycopg.Connection, incident_id: str) -> IncidentContext:
    """Load an incident and all its linked events, oldest first."""
    incident = conn.execute(
        """SELECT c.slug, i.environment, i.first_seen_at, i.last_seen_at
           FROM incidents i JOIN components c ON c.id = i.component_id
           WHERE i.id = %s""", (incident_id,)).fetchone()
    if incident is None:
        raise LookupError(f"incident {incident_id} not found")
    rows = conn.execute(
        """SELECT e.id::text, e.type, e.occurred_at, e.summary, e.severity, l.reason, e.details
           FROM incident_events l JOIN operational_events e ON e.id = l.event_id
           WHERE l.incident_id = %s
           ORDER BY e.occurred_at, e.id""", (incident_id,)).fetchall()
    slug, environment, first_seen, last_seen = incident
    return IncidentContext(
        incident_id=incident_id, component_slug=slug, environment=environment,
        first_seen_at=first_seen, last_seen_at=last_seen,
        events=tuple(EvidenceEvent(id=r[0], type=r[1], occurred_at=r[2], summary=r[3], severity=r[4],
                                   link_reason=r[5], details=r[6] or {}) for r in rows))


STEPS = ("collect_evidence", "draft_findings", "save_revision")


def set_step(conn: psycopg.Connection, job: Job, step: str) -> bool:
    """Record that the job entered `step`. Guarded by the lease: returns False
    (and changes nothing) if the lease was lost."""
    if step not in STEPS:
        raise ValueError(f"unknown step {step!r}")
    cur = conn.execute(
        """UPDATE analysis_jobs SET step = %s, step_started_at = now(), updated_at = now()
           WHERE id = %s AND status = 'running' AND lease_token = %s""",
        (step, job.id, job.lease_token))
    return cur.rowcount == 1


def write_revision(conn: psycopg.Connection, job: Job, findings: list[Finding], seen: Sequence[str],
                   summary: Sequence[Segment] = ()) -> str | None:
    """Mark the job succeeded and store its findings and summary as the
    incident's next revision, with the IDs of the events the analysis read
    (`seen`), in one transaction. Returns the revision ID, or None if the
    job's lease was lost (nothing is written)."""
    try:
        with conn.transaction():
            # Serializes revision numbering per incident.
            conn.execute("SELECT id FROM incidents WHERE id = %s FOR NO KEY UPDATE", (job.incident_id,))
            done = conn.execute(
                """UPDATE analysis_jobs
                   SET status = 'succeeded', lease_token = NULL, lease_expires_at = NULL,
                       finished_at = now(), updated_at = now()
                   WHERE id = %s AND status = 'running' AND lease_token = %s""",
                (job.id, job.lease_token))
            if done.rowcount != 1:
                raise _LostLease
            row = conn.execute(
                """INSERT INTO analysis_revisions (incident_id, job_id, revision_number, summary)
                   SELECT %s, %s, COALESCE(MAX(revision_number), 0) + 1, %s
                   FROM analysis_revisions WHERE incident_id = %s
                   ON CONFLICT (job_id) DO NOTHING
                   RETURNING id::text""",
                (job.incident_id, job.id, Jsonb(list(summary)), job.incident_id)).fetchone()
            if row is None:
                # Already written for this job; findings are immutable.
                return conn.execute("SELECT id::text FROM analysis_revisions WHERE job_id = %s",
                                    (job.id,)).fetchone()[0]
            revision_id = row[0]
            # One statement for all of them: this runs under the incident lock.
            conn.execute(
                """INSERT INTO revision_events (revision_id, event_id)
                   SELECT %s, e FROM unnest(%s::uuid[]) AS e""",
                (revision_id, list(dict.fromkeys(seen))))
            for ordinal, f in enumerate(findings):
                finding_id = conn.execute(
                    """INSERT INTO findings (revision_id, ordinal, kind, rule_id, text, limitations)
                       VALUES (%s, %s, %s, %s, %s, %s) RETURNING id""",
                    (revision_id, ordinal, f.kind, f.rule_id, f.text, f.limitations)).fetchone()[0]
                for event_id in dict.fromkeys(f.evidence_ids):
                    conn.execute("INSERT INTO finding_evidence (finding_id, event_id) VALUES (%s, %s)",
                                 (finding_id, event_id))
            return revision_id
    except _LostLease:
        return None


def error_message(error: BaseException) -> str:
    """'ClassName: message', first line only, at most MAX_ERROR_CHARS."""
    text = f"{type(error).__name__}: {error}"
    return text.splitlines()[0][:MAX_ERROR_CHARS]


def backoff_seconds(attempts: int) -> int:
    """Delay before the next attempt: 5s × 2^attempts, capped at 300s."""
    return min(BACKOFF_BASE_SECONDS * 2 ** attempts, BACKOFF_CAP_SECONDS)


def fail_job(conn: psycopg.Connection, job: Job, error: BaseException) -> None:
    """Record a failed attempt. Takes no incident lock; every update is
    guarded by the lease, so a stale worker changes nothing.

    - Out of attempts: the job fails with the real error.
    - Otherwise, if newer work is already pending for the incident, this job
      fails as superseded (the pending job will analyse everything).
    - Otherwise it is requeued with exponential backoff.
    """
    message = error_message(error)
    if job.attempts >= job.max_attempts:
        _mark_failed(conn, job, message)
    else:
        _requeue(conn, job, message)


def _mark_failed(conn: psycopg.Connection, job: Job, message: str) -> None:
    conn.execute(
        """UPDATE analysis_jobs
           SET status = 'failed', last_error = %s, lease_token = NULL, lease_expires_at = NULL,
               finished_at = now(), updated_at = now()
           WHERE id = %s AND status = 'running' AND lease_token = %s""",
        (message, job.id, job.lease_token))


def _requeue(conn: psycopg.Connection, job: Job, message: str, *, check_pending: bool = True) -> None:
    """Put the job back to pending, or mark it superseded if another job is
    already pending. check_pending=False skips the check (tests use it to
    force the unique-index fallback)."""
    if check_pending:
        pending = conn.execute(
            "SELECT 1 FROM analysis_jobs WHERE incident_id = %s AND status = 'pending' AND id <> %s",
            (job.incident_id, job.id)).fetchone()
        if pending:
            _mark_failed(conn, job, SUPERSEDED)
            return
    try:
        with conn.transaction():  # a savepoint if the caller is in a transaction
            conn.execute(
                """UPDATE analysis_jobs
                   SET status = 'pending', last_error = %s, lease_token = NULL, lease_expires_at = NULL,
                       next_attempt_at = now() + make_interval(secs => %s), updated_at = now()
                   WHERE id = %s AND status = 'running' AND lease_token = %s""",
                (message, backoff_seconds(job.attempts), job.id, job.lease_token))
    except errors.UniqueViolation:
        # A pending job appeared after the check (uq_jobs_one_pending).
        _mark_failed(conn, job, SUPERSEDED)
