package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/modules/tenancy"
)

// Signals page sizes.
const (
	DefaultSignalsLimit = 50
	MaxSignalsLimit     = 200
)

// IncidentRef names the incident a signal was grouped into.
type IncidentRef struct {
	ID     string `json:"id"`
	Number int64  `json:"number"`
}

// Signal is one alert event received for a project, with what grouping did
// with it. Incident is null when the alert reached no incident (for example
// a resolution that matched nothing open).
type Signal struct {
	EventID     string        `json:"event_id"`
	Type        string        `json:"type"`
	OccurredAt  time.Time     `json:"occurred_at"`
	ReceivedAt  time.Time     `json:"received_at"`
	Summary     string        `json:"summary"`
	Severity    *string       `json:"severity"`
	Component   ListComponent `json:"component"`
	Environment string        `json:"environment"`
	Disposition string        `json:"disposition"`
	Incident    *IncidentRef  `json:"incident"`
	SourceURL   *string       `json:"source_url"`
}

// SignalsPage is one page of a project's signals.
type SignalsPage struct {
	Items      []Signal `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

// Signals returns a page of a project's alert.triggered and alert.resolved
// events, newest occurred_at first. Checks run in this order: project
// visible to orgID (ErrProjectNotFound), then limit and cursor (*httpx.Error).
func (s Service) Signals(ctx context.Context, orgID, projectID, limit, rawCursor string) (SignalsPage, error) {
	if err := tenancy.ProjectInOrg(ctx, s.Pool, orgID, projectID); err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return SignalsPage{}, ErrProjectNotFound
		}
		return SignalsPage{}, err
	}
	n, rej := parseLimit(limit, DefaultSignalsLimit, MaxSignalsLimit)
	if rej != nil {
		return SignalsPage{}, rej
	}
	after, rej := parseCursor(rawCursor)
	if rej != nil {
		return SignalsPage{}, rej
	}

	rows, err := projectSignals(ctx, s.Pool, orgID, projectID, after, n+1)
	if err != nil {
		return SignalsPage{}, err
	}
	page := SignalsPage{Items: make([]Signal, 0, min(len(rows), n))}
	for i, r := range rows {
		if i == n {
			last := rows[n-1]
			next := encodeCursor(cursor{At: last.OccurredAt, ID: last.EventID})
			page.NextCursor = &next
			break
		}
		sig := Signal{
			EventID: r.EventID, Type: r.Type, Summary: r.Summary, Severity: r.Severity,
			OccurredAt: r.OccurredAt.UTC(), ReceivedAt: r.ReceivedAt.UTC(),
			Component:   ListComponent{Slug: r.ComponentSlug, DisplayName: r.ComponentName},
			Environment: r.Environment, Disposition: r.Disposition, SourceURL: r.SourceURL,
		}
		if r.IncidentID != nil && r.IncidentNumber != nil {
			sig.Incident = &IncidentRef{ID: *r.IncidentID, Number: *r.IncidentNumber}
		}
		page.Items = append(page.Items, sig)
	}
	return page, nil
}
