// Package integrations manages the connections that send events to
// Sentinel: their capability, credential and component mappings.
package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/tenancy"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// ErrNotFound means the integration (or its project) does not exist or
// belongs to another organization.
var ErrNotFound = errors.New("integration not found")

// Mapping ties an external component ID in one environment to a Sentinel component.
type Mapping struct {
	ID                  string `json:"id"`
	ExternalComponentID string `json:"external_component_id"`
	Environment         string `json:"environment"`
	ComponentID         string `json:"component_id"`
}

// MappingInput is the body of a mapping request.
type MappingInput struct {
	ExternalComponentID string `json:"external_component_id"`
	Environment         string `json:"environment"`
	ComponentID         string `json:"component_id"`
}

// Integration is the API view of an integration. It never carries the
// credential or its hash.
type Integration struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Name       string    `json:"name"`
	Capability string    `json:"capability"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	Mappings   []Mapping `json:"mappings"`
}

// WithCredential is returned exactly twice in an integration's life: on
// create and on rotate.
type WithCredential struct {
	Integration
	Credential string `json:"credential"`
}

// CreateInput is the body of an integration create request.
type CreateInput struct {
	Name       string         `json:"name"`
	Capability string         `json:"capability"`
	Mappings   []MappingInput `json:"mappings"`
}

// Service manages integrations.
type Service struct {
	Pool *pgxpool.Pool
}

func invalidMapping(format string, args ...any) *httpx.Error {
	return &httpx.Error{Status: http.StatusBadRequest, Code: "invalid_mapping", Message: fmt.Sprintf(format, args...)}
}

// Create creates an integration and its mappings in one transaction and
// returns the plaintext credential once.
func (s Service) Create(ctx context.Context, orgID, projectID string, in CreateInput) (WithCredential, error) {
	switch {
	case strings.TrimSpace(in.Name) == "":
		return WithCredential{}, &httpx.Error{Status: http.StatusBadRequest, Code: "missing_field", Message: "missing required field name"}
	case in.Capability != domain.CapabilityIncidentInput && in.Capability != domain.CapabilityChangeSource:
		return WithCredential{}, &httpx.Error{Status: http.StatusBadRequest, Code: "invalid_field", Message: "field capability must be incident_input or change_source"}
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WithCredential{}, err
	}
	defer tx.Rollback(ctx)

	if err := tenancy.ProjectInOrg(ctx, tx, orgID, projectID); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return WithCredential{}, ErrNotFound
		}
		return WithCredential{}, err
	}
	credential, hash := auth.NewToken(domain.IntegrationTokenPrefix)
	created, err := insertIntegration(ctx, tx, orgID, projectID, in.Name, in.Capability, hash)
	if err != nil {
		return WithCredential{}, err
	}
	for _, m := range in.Mappings {
		added, err := addMapping(ctx, tx, orgID, created, m)
		if err != nil {
			return WithCredential{}, err
		}
		created.Mappings = append(created.Mappings, added)
	}
	if err := tx.Commit(ctx); err != nil {
		return WithCredential{}, err
	}
	return WithCredential{Integration: created, Credential: credential}, nil
}

// List returns a project's integrations with their mappings.
func (s Service) List(ctx context.Context, orgID, projectID string) ([]Integration, error) {
	if err := tenancy.ProjectInOrg(ctx, s.Pool, orgID, projectID); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	items, err := listIntegrations(ctx, s.Pool, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return items, s.attachMappings(ctx, items)
}

// SetEnabled enables or disables an integration. A disabled integration
// cannot ingest.
func (s Service) SetEnabled(ctx context.Context, orgID, integrationID string, enabled bool) (Integration, error) {
	if !httpx.IsUUID(integrationID) {
		return Integration{}, ErrNotFound
	}
	in, err := setEnabled(ctx, s.Pool, orgID, integrationID, enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, err
	}
	items := []Integration{in}
	if err := s.attachMappings(ctx, items); err != nil {
		return Integration{}, err
	}
	return items[0], nil
}

// RotateCredential replaces the credential. The old one stops working at once.
func (s Service) RotateCredential(ctx context.Context, orgID, integrationID string) (WithCredential, error) {
	if !httpx.IsUUID(integrationID) {
		return WithCredential{}, ErrNotFound
	}
	credential, hash := auth.NewToken(domain.IntegrationTokenPrefix)
	in, err := setCredentialHash(ctx, s.Pool, orgID, integrationID, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return WithCredential{}, ErrNotFound
	}
	if err != nil {
		return WithCredential{}, err
	}
	items := []Integration{in}
	if err := s.attachMappings(ctx, items); err != nil {
		return WithCredential{}, err
	}
	return WithCredential{Integration: items[0], Credential: credential}, nil
}

// AddMapping adds one component mapping to an existing integration.
func (s Service) AddMapping(ctx context.Context, orgID, integrationID string, m MappingInput) (Mapping, error) {
	if !httpx.IsUUID(integrationID) {
		return Mapping{}, ErrNotFound
	}
	in, err := integrationInOrg(ctx, s.Pool, orgID, integrationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mapping{}, ErrNotFound
	}
	if err != nil {
		return Mapping{}, err
	}
	return addMapping(ctx, s.Pool, orgID, in, m)
}

// addMapping validates and stores a mapping for in. The component must be
// in the integration's project and in the mapping's environment, so events
// can never be filed under the wrong environment.
func addMapping(ctx context.Context, q database.DBTX, orgID string, in Integration, m MappingInput) (Mapping, error) {
	switch {
	case strings.TrimSpace(m.ExternalComponentID) == "":
		return Mapping{}, invalidMapping("external_component_id is required")
	case !tenancy.EnvironmentPattern.MatchString(m.Environment):
		return Mapping{}, invalidMapping("environment must be 1–32 characters of lowercase letters, digits, '-' and '_'")
	case !httpx.IsUUID(m.ComponentID):
		return Mapping{}, invalidMapping("component_id must be a component ID")
	}
	componentEnv, err := componentEnvironment(ctx, q, orgID, in.ProjectID, m.ComponentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mapping{}, invalidMapping("component %s is not in this integration's project", m.ComponentID)
	}
	if err != nil {
		return Mapping{}, err
	}
	if componentEnv != m.Environment {
		return Mapping{}, invalidMapping("component %s is in environment '%s', but the mapping is for environment '%s'",
			m.ComponentID, componentEnv, m.Environment)
	}
	added, err := insertMapping(ctx, q, in.ID, m)
	if _, dup := database.UniqueViolation(err); dup {
		return Mapping{}, &httpx.Error{Status: http.StatusConflict, Code: "mapping_exists",
			Message: fmt.Sprintf("external_component_id '%s' is already mapped in environment '%s'", m.ExternalComponentID, m.Environment)}
	}
	return added, err
}

func (s Service) attachMappings(ctx context.Context, items []Integration) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	for i, in := range items {
		ids[i] = in.ID
	}
	byID, err := mappingsFor(ctx, s.Pool, ids)
	if err != nil {
		return err
	}
	for i := range items {
		if ms := byID[items[i].ID]; ms != nil {
			items[i].Mappings = ms
		}
	}
	return nil
}
