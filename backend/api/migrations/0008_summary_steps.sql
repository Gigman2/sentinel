-- +goose Up
-- The step a job is on (or failed at), and when it entered it. A retry
-- starts again from collect_evidence.
ALTER TABLE analysis_jobs
    ADD COLUMN step text CHECK (step IN ('collect_evidence', 'draft_findings', 'save_revision')),
    ADD COLUMN step_started_at timestamptz;

-- A revision's summary: [{"text": ..., "kind": "text"|"time"|"strong"}],
-- built by the worker only from the revision's findings. Revisions written
-- before this have none.
ALTER TABLE analysis_revisions ADD COLUMN summary jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE analysis_revisions DROP COLUMN summary;
ALTER TABLE analysis_jobs
    DROP COLUMN step_started_at,
    DROP COLUMN step;
