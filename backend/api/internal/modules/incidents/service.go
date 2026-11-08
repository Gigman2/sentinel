// Package incidents owns incident grouping, evidence linking and the
// engineer-facing incident views.
package incidents

import (
	"context"
	"errors"
	"time"

	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/tenancy"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// The evidence window around an incident: stored events for the same
// component from WindowBefore the first alert to WindowAfter the last one
// are relevant (relevant, not causal). V0 defaults; see the ADR.
const (
	WindowBefore = 30 * time.Minute
	WindowAfter  = 15 * time.Minute
)

// EvidenceWindow returns the inclusive window investigated for an incident.
func EvidenceWindow(firstSeen, lastSeen time.Time) (start, end time.Time) {
	return firstSeen.Add(-WindowBefore).UTC(), lastSeen.Add(WindowAfter).UTC()
}

// ErrNotFound means the incident does not exist or belongs to another organization.
var ErrNotFound = errors.New("incident not found")

// Component identifies an incident's primary component.
type Component struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// Window is a time range, inclusive at both ends.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Resolution records how an incident was resolved.
type Resolution struct {
	Source string  `json:"source"`
	Note   *string `json:"note"`
}

// Incident is the engineer-facing view of an incident.
type Incident struct {
	ID             string      `json:"id"`
	ProjectID      string      `json:"project_id"`
	Component      Component   `json:"component"`
	Environment    string      `json:"environment"`
	Fingerprint    string      `json:"fingerprint"`
	Status         string      `json:"status"`
	Severity       string      `json:"severity"`
	Title          string      `json:"title"`
	FirstSeenAt    time.Time   `json:"first_seen_at"`
	LastSeenAt     time.Time   `json:"last_seen_at"`
	EvidenceWindow Window      `json:"evidence_window"`
	ResolvedAt     *time.Time  `json:"resolved_at"`
	Resolution     *Resolution `json:"resolution"`
	TriggerEventID string      `json:"trigger_event_id"`
}

// Resolution sources.
const (
	ResolutionAlert  = "alert"
	ResolutionManual = "manual"
)

// MaxNoteRunes is the longest manual resolution note accepted.
const MaxNoteRunes = 4000

// Listing page sizes.
const (
	DefaultListLimit = 50
	MaxListLimit     = 100
)

// ErrAlreadyResolved means a manual resolve found the incident already resolved.
var ErrAlreadyResolved = errors.New("incident already resolved")

// ErrProjectNotFound means the project does not exist in the caller's organization.
var ErrProjectNotFound = errors.New("project not found")

// Service serves incident reads and manual resolution.
type Service struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

// Get returns an incident visible to orgID, or ErrNotFound.
func (s Service) Get(ctx context.Context, orgID, incidentID string) (Incident, error) {
	r, err := incidentInOrg(ctx, s.Pool, orgID, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}

	start, end := EvidenceWindow(r.FirstSeenAt, r.LastSeenAt)
	inc := Incident{
		ID:             r.ID,
		ProjectID:      r.ProjectID,
		Component:      Component{ID: r.ComponentID, Slug: r.ComponentSlug, DisplayName: r.ComponentName},
		Environment:    r.Environment,
		Fingerprint:    r.Fingerprint,
		Status:         r.Status,
		Severity:       r.Severity,
		Title:          r.Title,
		FirstSeenAt:    r.FirstSeenAt.UTC(),
		LastSeenAt:     r.LastSeenAt.UTC(),
		EvidenceWindow: Window{Start: start, End: end},
		TriggerEventID: r.TriggerEventID,
	}
	if r.ResolvedAt != nil {
		t := r.ResolvedAt.UTC()
		inc.ResolvedAt = &t
	}
	if r.ResolutionSource != nil {
		inc.Resolution = &Resolution{Source: *r.ResolutionSource, Note: r.ResolutionNote}
	}
	return inc, nil
}

// Resolve manually resolves an open incident visible to orgID. A note longer
// than MaxNoteRunes is an *httpx.Error; an empty note is stored as none.
func (s Service) Resolve(ctx context.Context, orgID, incidentID, note string) (Incident, error) {
	if utf8.RuneCountInString(note) > MaxNoteRunes {
		return Incident{}, &httpx.Error{Status: http.StatusBadRequest, Code: "invalid_note",
			Message: "note must be at most 4000 characters"}
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback(ctx)

	status, err := incidentStatusInOrgForUpdate(ctx, tx, orgID, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	if status == domain.IncidentResolved {
		return Incident{}, ErrAlreadyResolved
	}
	now := s.Now().UTC()
	if err := markIncidentResolved(ctx, tx, incidentID, now, ResolutionManual, notePtr, now); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return s.Get(ctx, orgID, incidentID)
}

// ListItem is one incident in a project listing.
type ListItem struct {
	ID          string        `json:"id"`
	Status      string        `json:"status"`
	Severity    string        `json:"severity"`
	Title       string        `json:"title"`
	Component   ListComponent `json:"component"`
	Environment string        `json:"environment"`
	FirstSeenAt time.Time     `json:"first_seen_at"`
	LastSeenAt  time.Time     `json:"last_seen_at"`
}

// ListComponent names an incident's component in a listing.
type ListComponent struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// Page is one page of a listing. NextCursor is null on the last page.
type Page struct {
	Items      []ListItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// List returns a page of a project's incidents, newest last_seen_at first.
// The raw query values are checked in this order: project visible to orgID
// (ErrProjectNotFound), then status, limit and cursor (each an *httpx.Error).
func (s Service) List(ctx context.Context, orgID, projectID, status, limit, rawCursor string) (Page, error) {
	if err := tenancy.ProjectInOrg(ctx, s.Pool, orgID, projectID); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return Page{}, ErrProjectNotFound
		}
		return Page{}, err
	}
	if status != "" && status != domain.IncidentOpen && status != domain.IncidentResolved {
		return Page{}, badRequest("invalid_status", "status must be open or resolved")
	}
	n, rej := parseLimit(limit, DefaultListLimit, MaxListLimit)
	if rej != nil {
		return Page{}, rej
	}
	after, rej := parseCursor(rawCursor)
	if rej != nil {
		return Page{}, rej
	}

	rows, err := listIncidents(ctx, s.Pool, orgID, projectID, status, after, n+1)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]ListItem, 0, min(len(rows), n))}
	for i, r := range rows {
		if i == n {
			last := rows[n-1]
			next := encodeCursor(cursor{At: last.LastSeenAt, ID: last.ID})
			page.NextCursor = &next
			break
		}
		page.Items = append(page.Items, ListItem{
			ID: r.ID, Status: r.Status, Severity: r.Severity, Title: r.Title,
			Component:   ListComponent{Slug: r.ComponentSlug, DisplayName: r.ComponentName},
			Environment: r.Environment, FirstSeenAt: r.FirstSeenAt.UTC(), LastSeenAt: r.LastSeenAt.UTC(),
		})
	}
	return page, nil
}

// parseLimit reads a page size: def when raw is empty, else an integer in [1, max].
func parseLimit(raw string, def, max int) (int, *httpx.Error) {
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 || v > max {
		return 0, badRequest("invalid_limit", fmt.Sprintf("limit must be an integer from 1 to %d", max))
	}
	return v, nil
}

// parseCursor reads an optional next_cursor value.
func parseCursor(raw string) (*cursor, *httpx.Error) {
	if raw == "" {
		return nil, nil
	}
	c, ok := decodeCursor(raw)
	if !ok {
		return nil, badRequest("invalid_cursor", "cursor is not a value returned as next_cursor")
	}
	return &c, nil
}

func badRequest(code, message string) *httpx.Error {
	return &httpx.Error{Status: http.StatusBadRequest, Code: code, Message: message}
}

// A cursor is base64url (unpadded) of "<RFC 3339 nano time>|<uuid>".
func encodeCursor(c cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.At.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

func decodeCursor(s string) (cursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || !httpx.IsUUID(id) {
		return cursor{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return cursor{}, false
	}
	return cursor{At: t, ID: id}, true
}
