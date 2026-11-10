-- +goose Up
-- When a job was first leased and when it reached succeeded or failed.
-- started_at is set once and survives retries; finished_at is set on the
-- terminal transition.
ALTER TABLE analysis_jobs
    ADD COLUMN started_at  timestamptz,
    ADD COLUMN finished_at timestamptz;

-- Jobs that already finished stopped changing then, so updated_at is when
-- they finished. When they started was never recorded and stays unknown.
UPDATE analysis_jobs SET finished_at = updated_at WHERE status IN ('succeeded', 'failed');

-- +goose Down
ALTER TABLE analysis_jobs
    DROP COLUMN finished_at,
    DROP COLUMN started_at;
