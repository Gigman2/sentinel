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
	"github.com/Gigman2/sentinel/backend/api/internal/modules/analysis"
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

// User names a person in a view.
type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// Incident is the engineer-facing view of an incident.
type Incident struct {
	ID             string        `json:"id"`
	Number         int64         `json:"number"`
	ProjectID      string        `json:"project_id"`
	Component      Component     `json:"component"`
	Environment    string        `json:"environment"`
	Fingerprint    string        `json:"fingerprint"`
	Status         string        `json:"status"`
	Severity       string        `json:"severity"`
	Title          string        `json:"title"`
	FirstSeenAt    time.Time     `json:"first_seen_at"`
	LastSeenAt     time.Time     `json:"last_seen_at"`
	EvidenceWindow Window        `json:"evidence_window"`
	ResolvedAt     *time.Time    `json:"resolved_at"`
	Resolution     *Resolution   `json:"resolution"`
	ResolvedBy     *User         `json:"resolved_by"`
	TriggerEventID string        `json:"trigger_event_id"`
	SignalCount    int           `json:"signal_count"`
	Analysis       analysis.View `json:"analysis"`
	// EvidenceSources is the project's integrations, then Logs and Metrics.
	EvidenceSources []EvidenceSource `json:"evidence_sources"`
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

// Get returns an incident visible to orgID, with its analysis, or
// ErrNotFound. Both are read in one REPEATABLE READ READ ONLY transaction so
// they come from the same snapshot.
func (s Service) Get(ctx context.Context, orgID, incidentID string) (Incident, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback(ctx)

	r, err := incidentInOrg(ctx, tx, orgID, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	view, err := analysis.Read(ctx, tx, incidentID)
	if err != nil {
		return Incident{}, err
	}
	sources, err := evidenceSources(ctx, tx, r.ProjectID, incidentID)
	if err != nil {
		return Incident{}, err
	}

	start, end := EvidenceWindow(r.FirstSeenAt, r.LastSeenAt)
	inc := Incident{
		ID:              r.ID,
		Number:          r.Number,
		ProjectID:       r.ProjectID,
		Component:       Component{ID: r.ComponentID, Slug: r.ComponentSlug, DisplayName: r.ComponentName},
		Environment:     r.Environment,
		Fingerprint:     r.Fingerprint,
		Status:          r.Status,
		Severity:        r.Severity,
		Title:           r.Title,
		FirstSeenAt:     r.FirstSeenAt.UTC(),
		LastSeenAt:      r.LastSeenAt.UTC(),
		EvidenceWindow:  Window{Start: start, End: end},
		TriggerEventID:  r.TriggerEventID,
		SignalCount:     r.SignalCount,
		Analysis:        view,
		EvidenceSources: sources,
	}
	if r.ResolvedByID != nil && r.ResolvedByName != nil {
		inc.ResolvedBy = &User{ID: *r.ResolvedByID, DisplayName: *r.ResolvedByName}
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

// Resolve manually resolves an open incident visible to orgID on behalf of
// userID, who is recorded as the resolver. A note longer than MaxNoteRunes
// is an *httpx.Error; an empty note is stored as none.
func (s Service) Resolve(ctx context.Context, orgID, userID, incidentID, note string) (Incident, error) {
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
	if err := markIncidentResolved(ctx, tx, incidentID, now, ResolutionManual, notePtr, &userID, now); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Incident{}, err
	}
	return s.Get(ctx, orgID, incidentID)
}

// ListItem is one incident in a project listing. AnalysisStatus is derived
// as in the detail view: the newest job's status, or "none".
type ListItem struct {
	ID             string        `json:"id"`
	Number         int64         `json:"number"`
	Status         string        `json:"status"`
	Severity       string        `json:"severity"`
	Title          string        `json:"title"`
	Component      ListComponent `json:"component"`
	Environment    string        `json:"environment"`
	FirstSeenAt    time.Time     `json:"first_seen_at"`
	LastSeenAt     time.Time     `json:"last_seen_at"`
	AnalysisStatus string        `json:"analysis_status"`
	SignalCount    int           `json:"signal_count"`
	ResolutionNote *string       `json:"resolution_note"`
}

// ListComponent names an incident's component in a listing.
type ListComponent struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// Page is one page of a listing. NextCursor is null on the last page.
// OpenCount is the project's open incidents, whatever the filters.
type Page struct {
	Items      []ListItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
	OpenCount  int        `json:"open_count"`
}

// ListQuery holds a listing's raw query values, as sent.
type ListQuery struct {
	Status, Severity, Component, Since, Q, Limit, Cursor string
}

// MaxQueryRunes is the longest title search accepted.
const MaxQueryRunes = 200

// List returns a page of a project's incidents, newest last_seen_at first.
// The project must be visible to orgID (ErrProjectNotFound); then each
// query value is checked in the order status, severity, since, q, limit,
// cursor (each an *httpx.Error). An unknown component slug matches nothing.
// An empty value is no filter.
func (s Service) List(ctx context.Context, orgID, projectID string, lq ListQuery) (Page, error) {
	if err := tenancy.ProjectInOrg(ctx, s.Pool, orgID, projectID); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return Page{}, ErrProjectNotFound
		}
		return Page{}, err
	}
	f, rej := parseListFilter(lq)
	if rej != nil {
		return Page{}, rej
	}
	n, rej := parseLimit(lq.Limit, DefaultListLimit, MaxListLimit)
	if rej != nil {
		return Page{}, rej
	}
	after, rej := parseCursor(lq.Cursor)
	if rej != nil {
		return Page{}, rej
	}

	// One snapshot, so open_count agrees with the rows.
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := listIncidents(ctx, tx, orgID, projectID, f, after, n+1)
	if err != nil {
		return Page{}, err
	}
	open, err := openIncidentCount(ctx, tx, orgID, projectID)
	if err != nil {
		return Page{}, err
	}

	page := Page{Items: make([]ListItem, 0, min(len(rows), n)), OpenCount: open}
	for i, r := range rows {
		if i == n {
			last := rows[n-1]
			next := encodeCursor(cursor{At: last.LastSeenAt, ID: last.ID})
			page.NextCursor = &next
			break
		}
		status := analysis.StatusNone
		if r.AnalysisStatus != nil {
			status = *r.AnalysisStatus
		}
		page.Items = append(page.Items, ListItem{
			ID: r.ID, Number: r.Number, Status: r.Status, Severity: r.Severity, Title: r.Title,
			Component:   ListComponent{Slug: r.ComponentSlug, DisplayName: r.ComponentName},
			Environment: r.Environment, FirstSeenAt: r.FirstSeenAt.UTC(), LastSeenAt: r.LastSeenAt.UTC(),
			AnalysisStatus: status, SignalCount: r.SignalCount, ResolutionNote: r.ResolutionNote,
		})
	}
	return page, nil
}

// parseListFilter checks the filter values of a listing.
func parseListFilter(lq ListQuery) (listFilter, *httpx.Error) {
	f := listFilter{Status: lq.Status, Severity: lq.Severity, Component: lq.Component, Query: lq.Q}
	if f.Status != "" && f.Status != domain.IncidentOpen && f.Status != domain.IncidentResolved {
		return f, badRequest("invalid_status", "status must be open or resolved")
	}
	if f.Severity != "" && domain.SeverityRank(f.Severity) < 0 {
		return f, badRequest("invalid_severity", "severity must be one of info, warning, error, critical")
	}
	if lq.Since != "" {
		t, err := time.Parse(time.RFC3339, lq.Since)
		if err != nil {
			return f, badRequest("invalid_since", "since must be an RFC 3339 time")
		}
		f.Since = &t
	}
	if !utf8.ValidString(f.Query) || strings.ContainsRune(f.Query, 0) || utf8.RuneCountInString(f.Query) > MaxQueryRunes {
		return f, badRequest("invalid_q", fmt.Sprintf("q must be valid text of at most %d characters", MaxQueryRunes))
	}
	return f, nil
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
