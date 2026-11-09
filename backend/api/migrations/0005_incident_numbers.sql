-- +goose Up
-- Per-project incident numbers (INC-<n>). The counter row on projects is
-- the source of truth; it is row-locked by every insert that takes a number.
ALTER TABLE projects ADD COLUMN next_incident_number bigint NOT NULL DEFAULT 1;

ALTER TABLE incidents ADD COLUMN number bigint;

UPDATE incidents i
SET number = n.rn
FROM (SELECT id, row_number() OVER (PARTITION BY project_id ORDER BY first_seen_at, id) AS rn
      FROM incidents) n
WHERE n.id = i.id;

ALTER TABLE incidents ALTER COLUMN number SET NOT NULL;
ALTER TABLE incidents ADD CONSTRAINT uq_incidents_project_number UNIQUE (project_id, number);

UPDATE projects p
SET next_incident_number = COALESCE((SELECT max(number) FROM incidents WHERE project_id = p.id), 0) + 1;

-- Who resolved the incident by hand. NULL when an alert resolved it.
ALTER TABLE incidents ADD COLUMN resolved_by_user_id uuid REFERENCES users (id);

-- The project's alert signals, newest first (GET /v1/projects/{id}/signals).
CREATE INDEX ix_events_project_alerts
    ON operational_events (project_id, occurred_at DESC, id DESC)
    WHERE type IN ('alert.triggered', 'alert.resolved');

-- +goose Down
DROP INDEX ix_events_project_alerts;
ALTER TABLE incidents DROP COLUMN resolved_by_user_id;
ALTER TABLE incidents DROP CONSTRAINT uq_incidents_project_number;
ALTER TABLE incidents DROP COLUMN number;
ALTER TABLE projects DROP COLUMN next_incident_number;
