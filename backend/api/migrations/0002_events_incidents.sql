-- +goose Up
CREATE TABLE operational_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    project_id      uuid NOT NULL REFERENCES projects (id),
    integration_id  uuid NOT NULL REFERENCES integrations (id),
    component_id    uuid NOT NULL REFERENCES components (id),
    environment     text NOT NULL,
    type            text NOT NULL CHECK (type IN ('alert.triggered', 'alert.resolved', 'error.observed', 'deployment.completed')),
    source_event_id text NOT NULL,
    occurred_at     timestamptz NOT NULL,
    received_at     timestamptz NOT NULL,
    severity        text,
    fingerprint     text,
    summary         text NOT NULL DEFAULT '',
    source_url      text,
    attributes      jsonb NOT NULL DEFAULT '{}',
    details         jsonb NOT NULL DEFAULT '{}',
    -- What ingest did with the event. Alert events are inserted with '' and
    -- given their final value inside the same transaction; '' is never committed.
    disposition     text NOT NULL,
    incident_id     uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_events_source UNIQUE (integration_id, source_event_id)
);

CREATE INDEX ix_events_component_time
    ON operational_events (organization_id, project_id, environment, component_id, occurred_at);

CREATE TABLE incidents (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations (id),
    project_id        uuid NOT NULL REFERENCES projects (id),
    component_id      uuid NOT NULL REFERENCES components (id),
    environment       text NOT NULL,
    fingerprint       text NOT NULL,
    status            text NOT NULL CHECK (status IN ('open', 'resolved')),
    severity          text NOT NULL,
    title             text NOT NULL,
    first_seen_at     timestamptz NOT NULL,
    last_seen_at      timestamptz NOT NULL,
    trigger_event_id  uuid NOT NULL REFERENCES operational_events (id),
    resolved_at       timestamptz,
    resolution_source text CHECK (resolution_source IN ('alert', 'manual')),
    resolution_note   text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- At most one open incident per grouping key.
CREATE UNIQUE INDEX uq_incidents_open_key
    ON incidents (organization_id, project_id, environment, component_id, fingerprint)
    WHERE status = 'open';

CREATE TABLE incident_events (
    incident_id uuid NOT NULL REFERENCES incidents (id),
    event_id    uuid NOT NULL REFERENCES operational_events (id),
    reason      text NOT NULL CHECK (reason IN ('trigger', 'repeat_trigger', 'resolution', 'same_component_time_window')),
    linked_at   timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, event_id)
);

-- +goose Down
DROP TABLE incident_events;
DROP TABLE incidents;
DROP TABLE operational_events;
