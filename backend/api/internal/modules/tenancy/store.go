package tenancy

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

func insertProject(ctx context.Context, q database.DBTX, orgID, slug, name string) (Project, error) {
	var p Project
	err := q.QueryRow(ctx, `
		INSERT INTO projects (organization_id, slug, name) VALUES ($1, $2, $3)
		RETURNING id::text, slug, name, created_at`, orgID, slug, name).Scan(&p.ID, &p.Slug, &p.Name, &p.CreatedAt)
	return p, err
}

func listProjects(ctx context.Context, q database.DBTX, orgID string) ([]Project, error) {
	rows, err := q.Query(ctx, `
		SELECT id::text, slug, name, created_at FROM projects
		WHERE organization_id = $1 ORDER BY slug`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func projectExistsInOrg(ctx context.Context, q database.DBTX, orgID, projectID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND organization_id = $2)`,
		projectID, orgID).Scan(&exists)
	return exists, err
}

func insertComponent(ctx context.Context, q database.DBTX, orgID, projectID string, in ComponentInput) (Component, error) {
	var c Component
	err := q.QueryRow(ctx, `
		INSERT INTO components (organization_id, project_id, environment, slug, display_name, kind, description, owner)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+componentColumns,
		orgID, projectID, in.Environment, in.Slug, in.DisplayName, in.Kind, in.Description, in.Owner).Scan(componentDest(&c)...)
	return c, err
}

func listComponents(ctx context.Context, q database.DBTX, projectID string) ([]Component, error) {
	rows, err := q.Query(ctx, `
		SELECT `+componentColumns+`
		FROM components WHERE project_id = $1 ORDER BY environment, slug`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Component{}
	for rows.Next() {
		var c Component
		if err := rows.Scan(componentDest(&c)...); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

const componentColumns = `id::text, project_id::text, environment, slug, display_name, kind, description, owner, created_at`

func componentDest(c *Component) []any {
	return []any{&c.ID, &c.ProjectID, &c.Environment, &c.Slug, &c.DisplayName, &c.Kind, &c.Description, &c.Owner, &c.CreatedAt}
}

// componentScope returns a component's environment if it is in projectID.
// pgx.ErrNoRows otherwise.
func componentScope(ctx context.Context, q database.DBTX, projectID, componentID string) (string, error) {
	var env string
	err := q.QueryRow(ctx, `SELECT environment FROM components WHERE id = $1 AND project_id = $2`, componentID, projectID).Scan(&env)
	return env, err
}

func insertDependency(ctx context.Context, q database.DBTX, projectID, environment, from, to string) (time.Time, error) {
	var at time.Time
	err := q.QueryRow(ctx, `
		INSERT INTO component_dependencies (project_id, environment, from_component_id, to_component_id)
		VALUES ($1, $2, $3, $4) RETURNING created_at`, projectID, environment, from, to).Scan(&at)
	return at, err
}

func deleteDependency(ctx context.Context, q database.DBTX, projectID, from, to string) (bool, error) {
	tag, err := q.Exec(ctx, `
		DELETE FROM component_dependencies
		WHERE project_id = $1 AND from_component_id = $2 AND to_component_id = $3`, projectID, from, to)
	return tag.RowsAffected() == 1, err
}

const refColumns = `id::text, slug, display_name, kind`

func listDependencies(ctx context.Context, q database.DBTX, projectID string) ([]Dependency, error) {
	rows, err := q.Query(ctx, `
		SELECT f.id::text, f.slug, f.display_name, f.kind, t.id::text, t.slug, t.display_name, t.kind, d.environment, d.created_at
		FROM component_dependencies d
		JOIN components f ON f.id = d.from_component_id
		JOIN components t ON t.id = d.to_component_id
		WHERE d.project_id = $1
		ORDER BY d.environment, f.slug, t.slug`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Dependency, error) {
		var d Dependency
		err := row.Scan(&d.From.ID, &d.From.Slug, &d.From.DisplayName, &d.From.Kind,
			&d.To.ID, &d.To.Slug, &d.To.DisplayName, &d.To.Kind, &d.Environment, &d.CreatedAt)
		return d, err
	})
}

// downstream returns the components componentID calls, and the ones they
// call, two hops at most. Each component appears once, at its shortest
// depth, and the starting component never appears, so cycles end there.
func downstream(ctx context.Context, q database.DBTX, componentID string) ([]Downstream, error) {
	rows, err := q.Query(ctx, `
		WITH d1 AS (
		    SELECT to_component_id AS id FROM component_dependencies WHERE from_component_id = $1),
		d2 AS (
		    SELECT DISTINCT ON (e.to_component_id) e.to_component_id AS id, e.from_component_id AS via
		    FROM component_dependencies e
		    JOIN d1 ON d1.id = e.from_component_id
		    JOIN components v ON v.id = e.from_component_id
		    WHERE e.to_component_id <> $1 AND e.to_component_id NOT IN (SELECT id FROM d1)
		    ORDER BY e.to_component_id, v.slug)
		SELECT c.id::text, c.slug, c.display_name, c.kind, 1, NULL::text FROM d1 JOIN components c ON c.id = d1.id
		UNION ALL
		SELECT c.id::text, c.slug, c.display_name, c.kind, 2, d2.via::text FROM d2 JOIN components c ON c.id = d2.id
		ORDER BY 5, 2`, componentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Downstream, error) {
		var d Downstream
		err := row.Scan(&d.Component.ID, &d.Component.Slug, &d.Component.DisplayName, &d.Component.Kind, &d.Depth, &d.ViaComponentID)
		return d, err
	})
}
