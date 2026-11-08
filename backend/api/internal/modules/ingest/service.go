// Package ingest accepts events from authenticated integrations, normalizes
// them and records their effect (an incident created, evidence stored, …) in
// a single transaction.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/incidents"
)

// ErrInvalidCredential means the integration ID and token do not match.
var ErrInvalidCredential = errors.New("invalid integration credential")

// Integration is the authenticated sender of an event. Its organization and
// project scope every event it sends.
type Integration struct {
	ID             string
	OrganizationID string
	ProjectID      string
	Capability     string
	Enabled        bool
}

// Result is what ingest did with an event.
type Result struct {
	EventID     string  `json:"event_id"`
	Disposition string  `json:"disposition"`
	IncidentID  *string `json:"incident_id"`
	Replayed    bool    `json:"replayed"`
}

// Service accepts events.
type Service struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

// Authenticate returns the integration whose ID and credential both match,
// or ErrInvalidCredential.
func (s Service) Authenticate(ctx context.Context, integrationID, token string) (Integration, error) {
	in, err := integrationByCredential(ctx, s.Pool, integrationID, auth.Hash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrInvalidCredential
	}
	return in, err
}

// Accept maps, stores and applies one validated event. A client-side problem
// is returned as a *Rejection; anything else is an internal error. Nothing
// is acknowledged until the transaction commits.
func (s Service) Accept(ctx context.Context, in Integration, e Envelope) (Result, error) {
	componentID, err := mappedComponent(ctx, s.Pool, in.ID, e.ExternalComponentID, e.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, &Rejection{
			Status: http.StatusUnprocessableEntity,
			Code:   "unmapped_component",
			Message: fmt.Sprintf("no mapping for external_component_id '%s' in environment '%s'; add one via POST /v1/integrations/%s/mappings",
				e.ExternalComponentID, e.Environment, in.ID),
		}
	}
	if err != nil {
		return Result{}, fmt.Errorf("resolve mapping: %w", err)
	}

	ev := normalize(in, componentID, e, s.Now().UTC())

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)

	// Alerts get their final disposition from the incidents package below.
	if ev.ID, err = insertEvent(ctx, tx, ev, ""); err != nil {
		return Result{}, fmt.Errorf("store event: %w", err)
	}
	incidentID, _, err := incidents.ApplyAlertTriggered(ctx, tx, ev)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{EventID: ev.ID, Disposition: domain.DispositionIncidentCreated, IncidentID: &incidentID}, nil
}

// normalize builds the stored event. Scope comes from the integration and
// the mapping, never from the payload.
func normalize(in Integration, componentID string, e Envelope, receivedAt time.Time) domain.Event {
	attrs := e.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	return domain.Event{
		OrganizationID: in.OrganizationID,
		ProjectID:      in.ProjectID,
		IntegrationID:  in.ID,
		ComponentID:    componentID,
		Environment:    e.Environment,
		Type:           e.Type,
		SourceEventID:  e.SourceEventID,
		Severity:       e.Severity,
		Fingerprint:    e.Fingerprint,
		Summary:        e.Summary,
		SourceURL:      e.SourceURL,
		OccurredAt:     e.OccurredAt.UTC(),
		ReceivedAt:     receivedAt,
		Attributes:     attrs,
		Details:        map[string]any{},
	}
}
