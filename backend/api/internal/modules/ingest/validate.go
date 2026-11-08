package ingest

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Intake limits (V0 defaults; see the ADR).
const (
	MaxBodyBytes      = 64 << 10
	MaxIDRunes        = 200
	MaxAttributeKeys  = 50
	MaxFutureSkew     = 5 * time.Minute
	MaxEventAge       = 30 * 24 * time.Hour
	SchemaVersion     = "1"
	sourceEventIDName = "source_event_id"
)

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

// EventTime is when the event happened: occurred_at, or for deployments,
// deployed_at when occurred_at is absent.
func (e Envelope) EventTime() *time.Time {
	if e.OccurredAt == nil && e.Type == domain.EventDeploymentCompleted {
		return e.DeployedAt
	}
	return e.OccurredAt
}

// Rejection is a client error for a webhook event.
type Rejection = httpx.Error

func reject(code, message string) *Rejection {
	return &Rejection{Status: http.StatusBadRequest, Code: code, Message: message}
}

func missing(field string) *Rejection {
	return reject("missing_field", "missing required field "+field)
}

func invalid(field, why string) *Rejection {
	return reject("invalid_field", "field "+field+" "+why)
}

var eventTypes = []string{
	domain.EventAlertTriggered, domain.EventAlertResolved,
	domain.EventErrorObserved, domain.EventDeploymentCompleted,
}

// Validate checks an envelope completely: its shape, then its timestamps.
func Validate(e Envelope, now time.Time) *Rejection {
	if rej := ValidateShape(e); rej != nil {
		return rej
	}
	return ValidateTimes(e, now)
}

// ValidateShape checks everything except the timestamp range. Ingest checks
// for a replay between ValidateShape and ValidateTimes, so a resent event is
// answered from storage even after its timestamp has aged out.
func ValidateShape(e Envelope) *Rejection {
	if e.SchemaVersion != SchemaVersion {
		return reject("unsupported_schema_version", `schema_version must be "1"`)
	}
	if !slices.Contains(eventTypes, e.Type) {
		return reject("unsupported_event_type", "type must be one of alert.triggered, alert.resolved, error.observed, deployment.completed")
	}

	type field struct{ name, value string }
	required := []field{
		{sourceEventIDName, e.SourceEventID},
		{"external_component_id", e.ExternalComponentID},
		{"environment", e.Environment},
	}
	switch e.Type {
	case domain.EventAlertTriggered:
		required = append(required, field{"fingerprint", e.Fingerprint}, field{"severity", e.Severity}, field{"summary", e.Summary})
	case domain.EventAlertResolved:
		required = append(required, field{"fingerprint", e.Fingerprint}, field{"severity", e.Severity})
	case domain.EventDeploymentCompleted:
		required = append(required, field{"version", e.Version})
	}
	for _, f := range required {
		if f.value == "" {
			return missing(f.name)
		}
	}
	if e.EventTime() == nil {
		if e.Type == domain.EventDeploymentCompleted {
			return missing("occurred_at (or deployed_at)")
		}
		return missing("occurred_at")
	}

	if utf8.RuneCountInString(e.SourceEventID) > MaxIDRunes {
		return invalid(sourceEventIDName, "must be at most 200 characters")
	}
	if utf8.RuneCountInString(e.Fingerprint) > MaxIDRunes {
		return invalid("fingerprint", "must be at most 200 characters")
	}
	if e.Severity != "" && !slices.Contains(domain.Severities, e.Severity) {
		return invalid("severity", "must be one of info, warning, error, critical")
	}
	if e.SourceURL != "" && !isWebURL(e.SourceURL) {
		return reject("invalid_source_url", "source_url must be an absolute http or https URL")
	}
	if len(e.Attributes) > MaxAttributeKeys {
		return reject("attributes_too_large", "attributes may have at most 50 keys")
	}
	return nil
}

// ValidateTimes rejects events from too far in the future or the past. Both
// occurred_at and deployed_at are checked when present.
func ValidateTimes(e Envelope, now time.Time) *Rejection {
	for _, t := range []*time.Time{e.OccurredAt, e.DeployedAt} {
		if t == nil {
			continue
		}
		if t.After(now.Add(MaxFutureSkew)) {
			return reject("timestamp_in_future", "event time is more than 5 minutes in the future")
		}
		if t.Before(now.Add(-MaxEventAge)) {
			return reject("timestamp_too_old", "event time is more than 30 days in the past")
		}
	}
	return nil
}

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|api[_-]?key|cookie|session)`)

// SanitizeAttributes returns a copy of in without keys that look like
// credentials, at any depth, including inside arrays. The input is not modified.
func SanitizeAttributes(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if sensitiveKey.MatchString(k) {
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return SanitizeAttributes(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = sanitizeValue(item)
		}
		return out
	default:
		return v
	}
}
