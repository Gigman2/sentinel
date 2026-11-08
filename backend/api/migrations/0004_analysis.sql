-- +goose Up
CREATE TABLE analysis_jobs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id      uuid NOT NULL REFERENCES incidents (id),
    reason           text NOT NULL CHECK (reason IN ('incident_created', 'repeat_trigger', 'late_event', 'manual_retry')),
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    attempts         int NOT NULL DEFAULT 0,
    max_attempts     int NOT NULL DEFAULT 5,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    lease_token      uuid,
    lease_expires_at timestamptz,
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- At most one pending job per incident: new work coalesces into it.
CREATE UNIQUE INDEX uq_jobs_one_pending ON analysis_jobs (incident_id) WHERE status = 'pending';
CREATE INDEX ix_jobs_pending ON analysis_jobs (status, next_attempt_at);
CREATE INDEX ix_jobs_lease ON analysis_jobs (status, lease_expires_at);

-- An immutable analysis result. One per successful job.
CREATE TABLE analysis_revisions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id     uuid NOT NULL REFERENCES incidents (id),
    job_id          uuid NOT NULL UNIQUE REFERENCES analysis_jobs (id),
    revision_number int NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (incident_id, revision_number)
);

CREATE TABLE findings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    revision_id uuid NOT NULL REFERENCES analysis_revisions (id),
    ordinal     int NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('observation', 'hypothesis', 'unknown', 'next_check')),
    rule_id     text NOT NULL,
    text        text NOT NULL,
    limitations text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (revision_id, ordinal)
);

CREATE TABLE finding_evidence (
    finding_id uuid NOT NULL REFERENCES findings (id),
    event_id   uuid NOT NULL REFERENCES operational_events (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (finding_id, event_id)
);

-- +goose Down
DROP TABLE finding_evidence;
DROP TABLE findings;
DROP TABLE analysis_revisions;
DROP TABLE analysis_jobs;
