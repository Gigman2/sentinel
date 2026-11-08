package tenancy

import (
	"context"

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
		INSERT INTO components (organization_id, project_id, environment, slug, display_name, description, owner)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, project_id::text, environment, slug, display_name, description, owner, created_at`,
		orgID, projectID, in.Environment, in.Slug, in.DisplayName, in.Description, in.Owner).Scan(
		&c.ID, &c.ProjectID, &c.Environment, &c.Slug, &c.DisplayName, &c.Description, &c.Owner, &c.CreatedAt)
	return c, err
}

func listComponents(ctx context.Context, q database.DBTX, projectID string) ([]Component, error) {
	rows, err := q.Query(ctx, `
		SELECT id::text, project_id::text, environment, slug, display_name, description, owner, created_at
		FROM components WHERE project_id = $1 ORDER BY environment, slug`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Component{}
	for rows.Next() {
		var c Component
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Environment, &c.Slug, &c.DisplayName, &c.Description, &c.Owner, &c.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}
