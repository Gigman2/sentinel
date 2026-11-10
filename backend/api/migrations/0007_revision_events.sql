-- +goose Up
-- The events each revision's analysis read. A revision's new evidence is
-- what it saw that the previous revision did not, so history no longer
-- depends on comparing job start times with link times (which retries,
-- concurrent jobs and clock skew between API and database all break).
-- Revisions written before this table existed have no rows; their new
-- evidence is reported as unknown.
CREATE TABLE revision_events (
    revision_id uuid NOT NULL REFERENCES analysis_revisions (id),
    event_id    uuid NOT NULL REFERENCES operational_events (id),
    PRIMARY KEY (revision_id, event_id)
);

-- +goose Down
DROP TABLE revision_events;
