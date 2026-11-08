package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

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

// insertEventIfNew stores a normalized event with the given initial
// disposition. If the integration already sent this source_event_id it
// stores nothing and returns inserted=false. A concurrent insert of the same
// key waits for the other transaction and then reports inserted=false.
func insertEventIfNew(ctx context.Context, q database.DBTX, ev domain.Event, disposition string) (id string, inserted bool, err error) {
	err = q.QueryRow(ctx, `
		INSERT INTO operational_events (
			organization_id, project_id, integration_id, component_id, environment, type,
			source_event_id, occurred_at, received_at, severity, fingerprint, summary,
			source_url, attributes, details, disposition)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), $12,
		        NULLIF($13, ''), $14, $15, $16)
		ON CONFLICT ON CONSTRAINT uq_events_source DO NOTHING
		RETURNING id::text`,
		ev.OrganizationID, ev.ProjectID, ev.IntegrationID, ev.ComponentID, ev.Environment, ev.Type,
		ev.SourceEventID, ev.OccurredAt, ev.ReceivedAt, ev.Severity, ev.Fingerprint, ev.Summary,
		ev.SourceURL, ev.Attributes, ev.Details, disposition).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// storedResult returns what ingest recorded for an event the integration
// already sent. pgx.ErrNoRows means it has not been sent.
func storedResult(ctx context.Context, q database.DBTX, integrationID, sourceEventID string) (Result, error) {
	var r Result
	err := q.QueryRow(ctx, `
		SELECT id::text, disposition, incident_id::text
		FROM operational_events
		WHERE integration_id = $1 AND source_event_id = $2`, integrationID, sourceEventID).Scan(
		&r.EventID, &r.Disposition, &r.IncidentID)
	return r, err
}

// insertRejection records a configuration-related rejection. No payload is stored.
func insertRejection(ctx context.Context, q database.DBTX, in Integration, code string, e Envelope, receivedAt time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO intake_rejections (integration_id, organization_id, received_at, code,
		                               source_event_id, external_component_id, environment)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''))`,
		in.ID, in.OrganizationID, receivedAt, code, e.SourceEventID, e.ExternalComponentID, e.Environment)
	return err
}
