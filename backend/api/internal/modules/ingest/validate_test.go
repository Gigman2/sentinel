package ingest_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/modules/ingest"
)

func tp(t time.Time) *time.Time { return &t }

func baseAlert() ingest.Envelope {
	return ingest.Envelope{
		SchemaVersion: "1", SourceEventID: "alert-1", Type: "alert.triggered", OccurredAt: tp(now),
		ExternalComponentID: "payments-prod", Environment: "production",
		Severity: "critical", Fingerprint: "f", Summary: "s",
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ingest.Envelope)
		code   string // "" means valid
	}{
		{"valid alert.triggered", func(e *ingest.Envelope) {}, ""},
		{"valid alert.resolved", func(e *ingest.Envelope) { e.Type = "alert.resolved"; e.Summary = "" }, ""},
		{"valid error.observed without fingerprint", func(e *ingest.Envelope) { e.Type = "error.observed"; e.Fingerprint = ""; e.Severity = "" }, ""},
		{"valid deployment via deployed_at", func(e *ingest.Envelope) {
			e.Type, e.Version, e.OccurredAt, e.DeployedAt, e.Fingerprint, e.Severity = "deployment.completed", "1.8.4", nil, tp(now.Add(-7*time.Minute)), "", ""
		}, ""},
		{"multibyte source id at 200 runes", func(e *ingest.Envelope) { e.SourceEventID = strings.Repeat("é", 200) }, ""},
		{"https source url", func(e *ingest.Envelope) { e.SourceURL = "https://monitor.example/a/1" }, ""},
		{"exactly 50 attributes", func(e *ingest.Envelope) { e.Attributes = attrs(50) }, ""},
		{"5 minutes in the future is allowed", func(e *ingest.Envelope) { e.OccurredAt = tp(now.Add(5 * time.Minute)) }, ""},

		{"schema version", func(e *ingest.Envelope) { e.SchemaVersion = "2" }, "unsupported_schema_version"},
		{"event type", func(e *ingest.Envelope) { e.Type = "alert.exploded" }, "unsupported_event_type"},
		{"missing source id", func(e *ingest.Envelope) { e.SourceEventID = "" }, "missing_field"},
		{"source id too long", func(e *ingest.Envelope) { e.SourceEventID = strings.Repeat("é", 201) }, "invalid_field"},
		{"missing external component", func(e *ingest.Envelope) { e.ExternalComponentID = "" }, "missing_field"},
		{"missing environment", func(e *ingest.Envelope) { e.Environment = "" }, "missing_field"},
		{"missing occurred_at", func(e *ingest.Envelope) { e.OccurredAt = nil }, "missing_field"},
		{"too far in the future", func(e *ingest.Envelope) { e.OccurredAt = tp(now.Add(6 * time.Minute)) }, "timestamp_in_future"},
		{"too old", func(e *ingest.Envelope) { e.OccurredAt = tp(now.Add(-31 * 24 * time.Hour)) }, "timestamp_too_old"},
		{"alert missing fingerprint", func(e *ingest.Envelope) { e.Fingerprint = "" }, "missing_field"},
		{"fingerprint too long", func(e *ingest.Envelope) { e.Fingerprint = strings.Repeat("x", 201) }, "invalid_field"},
		{"alert missing severity", func(e *ingest.Envelope) { e.Severity = "" }, "missing_field"},
		{"bad severity", func(e *ingest.Envelope) { e.Severity = "fatal" }, "invalid_field"},
		{"bad severity on error.observed", func(e *ingest.Envelope) { e.Type = "error.observed"; e.Severity = "fatal" }, "invalid_field"},
		{"triggered missing summary", func(e *ingest.Envelope) { e.Summary = "" }, "missing_field"},
		{"deployment missing version", func(e *ingest.Envelope) { e.Type = "deployment.completed"; e.Version = "" }, "missing_field"},
		{"deployed_at checked too", func(e *ingest.Envelope) {
			e.Type, e.Version, e.DeployedAt = "deployment.completed", "1", tp(now.Add(-40*24*time.Hour))
		}, "timestamp_too_old"},
		{"javascript source url", func(e *ingest.Envelope) { e.SourceURL = "javascript:alert(1)" }, "invalid_source_url"},
		{"relative source url", func(e *ingest.Envelope) { e.SourceURL = "/alerts/1" }, "invalid_source_url"},
		{"51 attributes", func(e *ingest.Envelope) { e.Attributes = attrs(51) }, "attributes_too_large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := baseAlert()
			c.mutate(&e)
			rej := ingest.Validate(e, now)
			switch {
			case c.code == "" && rej != nil:
				t.Fatalf("want valid, got %s: %s", rej.Code, rej.Message)
			case c.code != "" && rej == nil:
				t.Fatalf("want %s, got valid", c.code)
			case c.code != "" && rej.Code != c.code:
				t.Fatalf("want %s, got %s: %s", c.code, rej.Code, rej.Message)
			case c.code == "missing_field" || c.code == "invalid_field":
				if !strings.Contains(rej.Message, "field") {
					t.Fatalf("message should name the field: %q", rej.Message)
				}
			}
		})
	}
}

func attrs(n int) map[string]any {
	m := map[string]any{}
	for i := 0; i < n; i++ {
		m[strings.Repeat("k", i+1)] = i
	}
	return m
}

func TestSanitizeAttributes(t *testing.T) {
	in := map[string]any{
		"error_rate": 0.18,
		"api_key":    "x",
		"Password":   "x",
		"nested":     map[string]any{"Authorization": "Bearer y", "ok": 1, "deeper": map[string]any{"session_id": "s", "keep": true}},
		"list":       []any{map[string]any{"token": "x", "name": "a"}, "plain", []any{map[string]any{"cookie": "c"}}},
	}
	want := map[string]any{
		"error_rate": 0.18,
		"nested":     map[string]any{"ok": 1, "deeper": map[string]any{"keep": true}},
		"list":       []any{map[string]any{"name": "a"}, "plain", []any{map[string]any{}}},
	}
	if got := ingest.SanitizeAttributes(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if _, ok := in["api_key"]; !ok {
		t.Fatal("SanitizeAttributes must not modify its input")
	}
}
