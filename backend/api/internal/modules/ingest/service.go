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

// Replay returns the stored outcome if the integration already sent this
// source_event_id. Nothing is re-applied.
func (s Service) Replay(ctx context.Context, in Integration, sourceEventID string) (Result, bool, error) {
	r, err := storedResult(ctx, s.Pool, in.ID, sourceEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	r.Replayed = true
	return r, true, nil
}

// allowed reports whether an integration capability may send an event type.
func allowed(capability, eventType string) bool {
	switch capability {
	case domain.CapabilityIncidentInput:
		return eventType == domain.EventAlertTriggered || eventType == domain.EventAlertResolved || eventType == domain.EventErrorObserved
	case domain.CapabilityChangeSource:
		return eventType == domain.EventDeploymentCompleted
	}
	return false
}

// Accept checks capability and mapping, then stores and applies one
// validated event in a single transaction. A client-side problem is returned
// as a *Rejection; configuration rejections are also recorded. Nothing is
// acknowledged until the transaction commits.
func (s Service) Accept(ctx context.Context, in Integration, e Envelope) (Result, error) {
	receivedAt := s.Now().UTC()

	if !allowed(in.Capability, e.Type) {
		return Result{}, s.recordRejection(ctx, in, e, receivedAt, &Rejection{
			Status:  http.StatusForbidden,
			Code:    "capability_not_allowed",
			Message: fmt.Sprintf("a %s integration cannot send %s events", in.Capability, e.Type),
		})
	}

	componentID, err := mappedComponent(ctx, s.Pool, in.ID, e.ExternalComponentID, e.Environment)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, s.recordRejection(ctx, in, e, receivedAt, &Rejection{
			Status: http.StatusUnprocessableEntity,
			Code:   "unmapped_component",
			Message: fmt.Sprintf("no mapping for external_component_id '%s' in environment '%s'; add one via POST /v1/integrations/%s/mappings",
				e.ExternalComponentID, e.Environment, in.ID),
		})
	}
	if err != nil {
		return Result{}, fmt.Errorf("resolve mapping: %w", err)
	}

	ev := normalize(in, componentID, e, receivedAt)

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)

	ev.ID, err = s.store(ctx, tx, ev)
	if errors.Is(err, errDuplicate) {
		// Another request with the same source_event_id won (or arrived
		// earlier); answer from what it stored.
		tx.Rollback(ctx)
		r, _, err := s.Replay(ctx, in, e.SourceEventID)
		return r, err
	}
	if err != nil {
		return Result{}, err
	}

	result := Result{EventID: ev.ID}
	switch ev.Type {
	case domain.EventAlertTriggered:
		incidentID, _, err := incidents.ApplyAlertTriggered(ctx, tx, ev)
		if err != nil {
			return Result{}, err
		}
		result.Disposition, result.IncidentID = domain.DispositionIncidentCreated, &incidentID
	default:
		result.Disposition = initialDisposition(ev.Type)
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return result, nil
}

var errDuplicate = errors.New("duplicate source_event_id")

// initialDisposition is what an event is stored with. Alerts get their final
// disposition from the incidents package in the same transaction; the empty
// value is never committed.
func initialDisposition(eventType string) string {
	switch eventType {
	case domain.EventAlertTriggered:
		return ""
	case domain.EventAlertResolved:
		// Matching a resolution to an open incident comes with the incident
		// lifecycle work (plan Task 6).
		return domain.DispositionUnmatchedResolution
	default:
		return domain.DispositionEvidence
	}
}

func (s Service) store(ctx context.Context, tx pgx.Tx, ev domain.Event) (string, error) {
	id, inserted, err := insertEventIfNew(ctx, tx, ev, initialDisposition(ev.Type))
	if err != nil {
		return "", fmt.Errorf("store event: %w", err)
	}
	if !inserted {
		return "", errDuplicate
	}
	return id, nil
}

func (s Service) recordRejection(ctx context.Context, in Integration, e Envelope, receivedAt time.Time, rej *Rejection) error {
	if err := insertRejection(ctx, s.Pool, in, rej.Code, e, receivedAt); err != nil {
		return fmt.Errorf("record %s rejection: %w", rej.Code, err)
	}
	return rej
}

// normalize builds the stored event. Scope comes from the integration and
// the mapping, never from the payload.
func normalize(in Integration, componentID string, e Envelope, receivedAt time.Time) domain.Event {
	details := map[string]any{}
	if e.Type == domain.EventDeploymentCompleted {
		for k, v := range map[string]string{"version": e.Version, "commit_sha": e.CommitSHA, "repository": e.Repository, "deployment_id": e.DeploymentID} {
			if v != "" {
				details[k] = v
			}
		}
		if e.DeployedAt != nil {
			details["deployed_at"] = e.DeployedAt.UTC().Format(time.RFC3339)
		}
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
		OccurredAt:     e.EventTime().UTC(),
		ReceivedAt:     receivedAt,
		Attributes:     SanitizeAttributes(e.Attributes),
		Details:        details,
	}
}
