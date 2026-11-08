package ingest

import (
	"net/http"
	"slices"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// MaxBodyBytes caps a webhook payload.
const MaxBodyBytes = 64 << 10

// Envelope is the versioned generic webhook payload (PRD §7.2).
type Envelope struct {
	SchemaVersion       string         `json:"schema_version"`
	SourceEventID       string         `json:"source_event_id"`
	Type                string         `json:"type"`
	OccurredAt          *time.Time     `json:"occurred_at"`
	ExternalComponentID string         `json:"external_component_id"`
	Environment         string         `json:"environment"`
	Severity            string         `json:"severity"`
	Fingerprint         string         `json:"fingerprint"`
	Summary             string         `json:"summary"`
	SourceURL           string         `json:"source_url"`
	Attributes          map[string]any `json:"attributes"`

	// deployment.completed fields
	Version      string     `json:"version"`
	DeployedAt   *time.Time `json:"deployed_at"`
	CommitSHA    string     `json:"commit_sha"`
	Repository   string     `json:"repository"`
	DeploymentID string     `json:"deployment_id"`
}

// Rejection is a client error for a webhook event.
type Rejection = httpx.Error

func missing(field string) *Rejection {
	return &Rejection{Status: http.StatusBadRequest, Code: "missing_field", Message: "missing required field " + field}
}

func invalid(field, why string) *Rejection {
	return &Rejection{Status: http.StatusBadRequest, Code: "invalid_field", Message: "field " + field + " " + why}
}

// Validate checks an envelope's shape. It returns nil when the event is
// acceptable. This version accepts only alert.triggered; the other event
// types, timestamp limits and attribute rules arrive with plan Task 5.
func Validate(e Envelope, now time.Time) *Rejection {
	if e.SchemaVersion != "1" {
		return &Rejection{Status: http.StatusBadRequest, Code: "unsupported_schema_version", Message: `schema_version must be "1"`}
	}
	if e.Type != domain.EventAlertTriggered {
		return &Rejection{Status: http.StatusBadRequest, Code: "unsupported_event_type", Message: "unsupported event type " + e.Type}
	}
	for _, f := range []struct{ name, value string }{
		{"source_event_id", e.SourceEventID},
		{"external_component_id", e.ExternalComponentID},
		{"environment", e.Environment},
		{"fingerprint", e.Fingerprint},
		{"severity", e.Severity},
		{"summary", e.Summary},
	} {
		if f.value == "" {
			return missing(f.name)
		}
	}
	if e.OccurredAt == nil {
		return missing("occurred_at")
	}
	if !slices.Contains(domain.Severities, e.Severity) {
		return invalid("severity", "must be one of info, warning, error, critical")
	}
	return nil
}
