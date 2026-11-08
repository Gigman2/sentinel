-- +goose Up
-- Events an authenticated integration sent that Sentinel refused because of
-- configuration (an unmapped component, a capability the integration lacks),
-- so users can see when setup is broken. No payload is stored.
CREATE TABLE intake_rejections (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id        uuid NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    organization_id       uuid NOT NULL REFERENCES organizations (id),
    received_at           timestamptz NOT NULL,
    code                  text NOT NULL,
    source_event_id       text,
    external_component_id text,
    environment           text,
    created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ix_intake_rejections_integration_time ON intake_rejections (integration_id, received_at);

-- +goose Down
DROP TABLE intake_rejections;
