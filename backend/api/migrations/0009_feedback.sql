-- +goose Up
-- One verdict per engineer per finding (PRD UI-05). Feedback belongs to the
-- finding, so it stays with its revision and does not carry over to a new
-- revision's findings.
CREATE TABLE finding_feedback (
    finding_id uuid NOT NULL REFERENCES findings (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id),
    verdict    text NOT NULL CHECK (verdict IN ('useful', 'incorrect')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (finding_id, user_id)
);

-- A next check an engineer has done. No row means not done.
CREATE TABLE next_check_status (
    finding_id      uuid PRIMARY KEY REFERENCES findings (id) ON DELETE CASCADE,
    done_by_user_id uuid NOT NULL REFERENCES users (id),
    done_at         timestamptz NOT NULL
);

-- +goose Down
DROP TABLE next_check_status;
DROP TABLE finding_feedback;
