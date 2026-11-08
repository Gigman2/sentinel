package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Timeline page sizes.
const (
	DefaultTimelineLimit = 100
	MaxTimelineLimit     = 200
)

// TimelineItem is one event linked to an incident. The link reason says why
// it is shown; it never implies causality.
type TimelineItem struct {
	EventID    string         `json:"event_id"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	ReceivedAt time.Time      `json:"received_at"`
	Summary    string         `json:"summary"`
	Severity   *string        `json:"severity"`
	SourceURL  *string        `json:"source_url"`
	LinkReason string         `json:"link_reason"`
	Attributes map[string]any `json:"attributes"`
	Details    map[string]any `json:"details"`
}

// TimelinePage is one page of an incident's timeline.
type TimelinePage struct {
	Items      []TimelineItem `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

// Timeline returns a page of the events linked to an incident, oldest
// occurred_at first (ties by event ID). Checks run in this order: incident
// visible to orgID (ErrNotFound), then limit and cursor (*httpx.Error).
func (s Service) Timeline(ctx context.Context, orgID, incidentID, limit, rawCursor string) (TimelinePage, error) {
	if _, err := incidentInOrg(ctx, s.Pool, orgID, incidentID); errors.Is(err, pgx.ErrNoRows) {
		return TimelinePage{}, ErrNotFound
	} else if err != nil {
		return TimelinePage{}, err
	}
	n, rej := parseLimit(limit, DefaultTimelineLimit, MaxTimelineLimit)
	if rej != nil {
		return TimelinePage{}, rej
	}
	after, rej := parseCursor(rawCursor)
	if rej != nil {
		return TimelinePage{}, rej
	}

	rows, err := timelineEvents(ctx, s.Pool, incidentID, after, n+1)
	if err != nil {
		return TimelinePage{}, err
	}
	page := TimelinePage{Items: make([]TimelineItem, 0, min(len(rows), n))}
	for i, r := range rows {
		if i == n {
			last := rows[n-1]
			next := encodeCursor(cursor{At: last.OccurredAt, ID: last.EventID})
			page.NextCursor = &next
			break
		}
		page.Items = append(page.Items, TimelineItem{
			EventID: r.EventID, Type: r.Type, Summary: r.Summary, LinkReason: r.LinkReason,
			OccurredAt: r.OccurredAt.UTC(), ReceivedAt: r.ReceivedAt.UTC(),
			Severity: r.Severity, SourceURL: r.SourceURL,
			Attributes: nonNil(r.Attributes), Details: nonNil(r.Details),
		})
	}
	return page, nil
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
