package ingest

import (
	"context"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// integrationByCredential returns the integration only if both its ID and
// credential hash match. pgx.ErrNoRows means no match.
func integrationByCredential(ctx context.Context, q database.DBTX, integrationID, credentialHash string) (Integration, error) {
	var in Integration
	err := q.QueryRow(ctx, `
		SELECT id::text, organization_id::text, project_id::text, capability, enabled
		FROM integrations
		WHERE id = $1 AND credential_hash = $2`, integrationID, credentialHash).Scan(
		&in.ID, &in.OrganizationID, &in.ProjectID, &in.Capability, &in.Enabled)
	return in, err
}

// mappedComponent resolves an external component ID in an environment to a
// Sentinel component, through the integration's configured mappings.
// pgx.ErrNoRows means no mapping.
func mappedComponent(ctx context.Context, q database.DBTX, integrationID, externalComponentID, environment string) (string, error) {
	var componentID string
	err := q.QueryRow(ctx, `
		SELECT component_id::text
		FROM integration_component_mappings
		WHERE integration_id = $1 AND external_component_id = $2 AND environment = $3`,
		integrationID, externalComponentID, environment).Scan(&componentID)
	return componentID, err
}

// insertEvent stores a normalized event with the given initial disposition
// and returns its ID.
func insertEvent(ctx context.Context, q database.DBTX, ev domain.Event, disposition string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `
		INSERT INTO operational_events (
			organization_id, project_id, integration_id, component_id, environment, type,
			source_event_id, occurred_at, received_at, severity, fingerprint, summary,
			source_url, attributes, details, disposition)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), $12,
		        NULLIF($13, ''), $14, $15, $16)
		RETURNING id::text`,
		ev.OrganizationID, ev.ProjectID, ev.IntegrationID, ev.ComponentID, ev.Environment, ev.Type,
		ev.SourceEventID, ev.OccurredAt, ev.ReceivedAt, ev.Severity, ev.Fingerprint, ev.Summary,
		ev.SourceURL, ev.Attributes, ev.Details, disposition).Scan(&id)
	return id, err
}
