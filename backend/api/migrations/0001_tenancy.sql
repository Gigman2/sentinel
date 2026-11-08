-- +goose Up
CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email        text NOT NULL UNIQUE,
    display_name text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    organization_id uuid NOT NULL REFERENCES organizations (id),
    user_id         uuid NOT NULL REFERENCES users (id),
    role            text NOT NULL CHECK (role IN ('admin', 'member')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

CREATE TABLE api_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id),
    token_hash text NOT NULL UNIQUE,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    slug            text NOT NULL,
    name            text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug)
);

CREATE TABLE components (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    project_id      uuid NOT NULL REFERENCES projects (id),
    environment     text NOT NULL,
    slug            text NOT NULL,
    display_name    text NOT NULL,
    description     text,
    owner           text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, environment, slug)
);

CREATE TABLE integrations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id),
    project_id      uuid NOT NULL REFERENCES projects (id),
    name            text NOT NULL,
    capability      text NOT NULL CHECK (capability IN ('incident_input', 'change_source')),
    credential_hash text NOT NULL UNIQUE,
    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE integration_component_mappings (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id        uuid NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    external_component_id text NOT NULL,
    environment           text NOT NULL,
    component_id          uuid NOT NULL REFERENCES components (id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (integration_id, external_component_id, environment)
);

-- +goose Down
DROP TABLE integration_component_mappings;
DROP TABLE integrations;
DROP TABLE components;
DROP TABLE projects;
DROP TABLE api_tokens;
DROP TABLE memberships;
DROP TABLE users;
DROP TABLE organizations;
