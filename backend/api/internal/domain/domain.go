// Package domain holds the string constants shared across Sentinel's packages.
// The database CHECK constraints and the Python worker use the same values.
package domain

// Event types accepted by the generic webhook (schema_version "1").
const (
	EventAlertTriggered      = "alert.triggered"
	EventAlertResolved       = "alert.resolved"
	EventErrorObserved       = "error.observed"
	EventDeploymentCompleted = "deployment.completed"
)

// Incident statuses.
const (
	IncidentOpen     = "open"
	IncidentResolved = "resolved"
)

// Reasons an event is linked to an incident. A link records relevance, never causality.
const (
	LinkTrigger                 = "trigger"
	LinkRepeatTrigger           = "repeat_trigger"
	LinkResolution              = "resolution"
	LinkSameComponentTimeWindow = "same_component_time_window"
)

// What ingest did with an accepted event.
const (
	DispositionIncidentCreated     = "incident_created"
	DispositionIncidentAttached    = "incident_attached"
	DispositionIncidentResolved    = "incident_resolved"
	DispositionUnmatchedResolution = "unmatched_resolution"
	DispositionEvidence            = "evidence"
)

// Severities, lowest to highest.
var Severities = []string{"info", "warning", "error", "critical"}

// SeverityRank returns a severity's position in Severities, or -1 if unknown.
func SeverityRank(s string) int {
	for i, v := range Severities {
		if v == s {
			return i
		}
	}
	return -1
}

// Integration capabilities.
const (
	CapabilityIncidentInput = "incident_input"
	CapabilityChangeSource  = "change_source"
)

// Membership roles.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Token prefixes. Tokens are 32 random bytes, base64url-encoded, after the prefix.
const (
	UserTokenPrefix        = "sk_usr_"
	IntegrationTokenPrefix = "sk_int_"
)
