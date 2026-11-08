package integrations

import (
	"context"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

const integrationColumns = `id::text, project_id::text, name, capability, enabled, created_at`

func scanIntegration(row interface{ Scan(...any) error }) (Integration, error) {
	in := Integration{Mappings: []Mapping{}}
	err := row.Scan(&in.ID, &in.ProjectID, &in.Name, &in.Capability, &in.Enabled, &in.CreatedAt)
	return in, err
}

func insertIntegration(ctx context.Context, q database.DBTX, orgID, projectID, name, capability, credentialHash string) (Integration, error) {
	return scanIntegration(q.QueryRow(ctx, `
		INSERT INTO integrations (organization_id, project_id, name, capability, credential_hash)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+integrationColumns, orgID, projectID, name, capability, credentialHash))
}

// integrationInOrg returns the integration only if it belongs to orgID.
// pgx.ErrNoRows means not found.
func integrationInOrg(ctx context.Context, q database.DBTX, orgID, integrationID string) (Integration, error) {
	return scanIntegration(q.QueryRow(ctx, `
		SELECT `+integrationColumns+` FROM integrations
		WHERE id = $1 AND organization_id = $2`, integrationID, orgID))
}

func listIntegrations(ctx context.Context, q database.DBTX, orgID, projectID string) ([]Integration, error) {
	rows, err := q.Query(ctx, `
		SELECT `+integrationColumns+` FROM integrations
		WHERE organization_id = $1 AND project_id = $2
		ORDER BY created_at, id`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Integration{}
	for rows.Next() {
		in, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, in)
	}
	return items, rows.Err()
}

// setEnabled updates and returns the integration. pgx.ErrNoRows means not found in orgID.
func setEnabled(ctx context.Context, q database.DBTX, orgID, integrationID string, enabled bool) (Integration, error) {
	return scanIntegration(q.QueryRow(ctx, `
		UPDATE integrations SET enabled = $3
		WHERE id = $1 AND organization_id = $2
		RETURNING `+integrationColumns, integrationID, orgID, enabled))
}

// setCredentialHash replaces the stored credential hash. pgx.ErrNoRows means not found in orgID.
func setCredentialHash(ctx context.Context, q database.DBTX, orgID, integrationID, credentialHash string) (Integration, error) {
	return scanIntegration(q.QueryRow(ctx, `
		UPDATE integrations SET credential_hash = $3
		WHERE id = $1 AND organization_id = $2
		RETURNING `+integrationColumns, integrationID, orgID, credentialHash))
}

// componentEnvironment returns a component's environment, only if the
// component is in projectID of orgID. pgx.ErrNoRows means it is not.
func componentEnvironment(ctx context.Context, q database.DBTX, orgID, projectID, componentID string) (string, error) {
	var env string
	err := q.QueryRow(ctx, `
		SELECT environment FROM components
		WHERE id = $1 AND project_id = $2 AND organization_id = $3`, componentID, projectID, orgID).Scan(&env)
	return env, err
}

func insertMapping(ctx context.Context, q database.DBTX, integrationID string, m MappingInput) (Mapping, error) {
	var out Mapping
	err := q.QueryRow(ctx, `
		INSERT INTO integration_component_mappings (integration_id, external_component_id, environment, component_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, external_component_id, environment, component_id::text`,
		integrationID, m.ExternalComponentID, m.Environment, m.ComponentID).Scan(
		&out.ID, &out.ExternalComponentID, &out.Environment, &out.ComponentID)
	return out, err
}

// mappingsFor returns the mappings of the given integrations, keyed by integration ID.
func mappingsFor(ctx context.Context, q database.DBTX, integrationIDs []string) (map[string][]Mapping, error) {
	rows, err := q.Query(ctx, `
		SELECT integration_id::text, id::text, external_component_id, environment, component_id::text
		FROM integration_component_mappings
		WHERE integration_id = ANY($1::uuid[])
		ORDER BY environment, external_component_id`, integrationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Mapping{}
	for rows.Next() {
		var integrationID string
		var m Mapping
		if err := rows.Scan(&integrationID, &m.ID, &m.ExternalComponentID, &m.Environment, &m.ComponentID); err != nil {
			return nil, err
		}
		out[integrationID] = append(out[integrationID], m)
	}
	return out, rows.Err()
}
