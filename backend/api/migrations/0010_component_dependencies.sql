-- +goose Up
-- internal: a service the organisation runs; external: a third party it calls.
ALTER TABLE components ADD COLUMN kind text NOT NULL DEFAULT 'internal' CHECK (kind IN ('internal', 'external'));

-- Lets dependencies reference a component together with its project and
-- environment, so both ends are guaranteed to share them.
ALTER TABLE components ADD CONSTRAINT uq_components_scope UNIQUE (id, project_id, environment);

-- A registered "from calls to" edge. Registered by people; Phase 1 gathers
-- no evidence from dependencies.
CREATE TABLE component_dependencies (
    project_id        uuid NOT NULL REFERENCES projects (id),
    environment       text NOT NULL,
    from_component_id uuid NOT NULL,
    to_component_id   uuid NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (from_component_id, to_component_id),
    CHECK (from_component_id <> to_component_id),
    FOREIGN KEY (from_component_id, project_id, environment)
        REFERENCES components (id, project_id, environment) ON DELETE CASCADE,
    FOREIGN KEY (to_component_id, project_id, environment)
        REFERENCES components (id, project_id, environment) ON DELETE CASCADE
);
CREATE INDEX ix_component_dependencies_project ON component_dependencies (project_id, created_at);

-- +goose Down
DROP TABLE component_dependencies;
ALTER TABLE components DROP CONSTRAINT uq_components_scope;
ALTER TABLE components DROP COLUMN kind;
