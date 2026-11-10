package analysis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRevisionNotFound means the incident has no revision with that number.
var ErrRevisionNotFound = errors.New("revision not found")

// HistoryItem is one revision in an incident's history.
type HistoryItem struct {
	RevisionNumber int       `json:"revision_number"`
	CreatedAt      time.Time `json:"created_at"`
	Reason         string    `json:"reason"`
	FindingCount   int       `json:"finding_count"`
	// Changes is nil for revision 1, which has nothing to compare against.
	Changes *Changes `json:"changes"`
	// NewEvidence is nil when unknown: the revision, or the one before it,
	// was written before revisions recorded the events they read.
	NewEvidence []EvidenceLink `json:"new_evidence"`
}

// Changes is a multiset diff of (kind, rule_id) against the previous revision.
type Changes struct {
	Added   []RuleRef `json:"added"`
	Removed []RuleRef `json:"removed"`
}

// RuleRef names a finding by its kind and the rule that produced it.
type RuleRef struct {
	Kind   string `json:"kind"`
	RuleID string `json:"rule_id"`
}

// EvidenceLink is an event this revision read that the previous one did not.
type EvidenceLink struct {
	EventID    string    `json:"event_id"`
	Type       string    `json:"type"`
	ReceivedAt time.Time `json:"received_at"`
}

// History returns the incident's revisions, newest first.
//
// A revision's new evidence is the events its analysis read that the
// previous revision's did not (for revision 1, everything it read), taken
// from the sets the worker records. It does not depend on when jobs started
// or events were linked, so retries, overlapping jobs and clock skew cannot
// misplace it.
func (s Service) History(ctx context.Context, orgID, incidentID string) ([]HistoryItem, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if ok, err := incidentExistsInOrg(ctx, tx, orgID, incidentID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound
	}
	revs, err := revisionHistory(ctx, tx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("read revisions: %w", err)
	}
	keys, err := incidentFindingKeys(ctx, tx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("read finding keys: %w", err)
	}
	seenRows, err := revisionSeenEvents(ctx, tx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("read revision events: %w", err)
	}

	byRevision := map[string][]RuleRef{}
	for _, k := range keys {
		byRevision[k.RevisionID] = append(byRevision[k.RevisionID], RuleRef{Kind: k.Kind, RuleID: k.RuleID})
	}
	// Every analysis reads at least its trigger, so a revision with no
	// recorded events predates recording.
	seen := map[string][]EvidenceLink{}
	for _, r := range seenRows {
		seen[r.RevisionID] = append(seen[r.RevisionID], EvidenceLink{EventID: r.EventID, Type: r.Type, ReceivedAt: r.ReceivedAt.UTC()})
	}

	items := make([]HistoryItem, len(revs))
	for i, r := range revs {
		item := HistoryItem{
			RevisionNumber: r.RevisionNumber,
			CreatedAt:      r.CreatedAt.UTC(),
			Reason:         r.Reason,
			FindingCount:   len(byRevision[r.ID]),
		}
		if i > 0 {
			c := diffRules(byRevision[revs[i-1].ID], byRevision[r.ID])
			item.Changes = &c
		}
		if cur := seen[r.ID]; len(cur) > 0 {
			switch {
			case i == 0:
				item.NewEvidence = cur
			case len(seen[revs[i-1].ID]) > 0:
				item.NewEvidence = newSince(seen[revs[i-1].ID], cur)
			}
		}
		items[len(revs)-1-i] = item
	}
	return items, nil
}

// newSince returns the events in cur that are not in prev, in cur's order
// (never nil).
func newSince(prev, cur []EvidenceLink) []EvidenceLink {
	had := make(map[string]bool, len(prev))
	for _, e := range prev {
		had[e.EventID] = true
	}
	out := []EvidenceLink{}
	for _, e := range cur {
		if !had[e.EventID] {
			out = append(out, e)
		}
	}
	return out
}

// diffRules returns what was added and removed going from prev to next,
// counting duplicates (a rule that fires twice is two findings). Added keeps
// next's order and removed keeps prev's.
func diffRules(prev, next []RuleRef) Changes {
	remaining := map[RuleRef]int{}
	for _, r := range prev {
		remaining[r]++
	}
	c := Changes{Added: []RuleRef{}, Removed: []RuleRef{}}
	for _, r := range next {
		if remaining[r] > 0 {
			remaining[r]--
		} else {
			c.Added = append(c.Added, r)
		}
	}
	for _, r := range prev {
		if remaining[r] > 0 {
			remaining[r]--
			c.Removed = append(c.Removed, r)
		}
	}
	return c
}

// RevisionByNumber returns revision n of an incident visible to orgID.
func (s Service) RevisionByNumber(ctx context.Context, orgID, incidentID string, n int) (Revision, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx)

	if ok, err := incidentExistsInOrg(ctx, tx, orgID, incidentID); err != nil {
		return Revision{}, err
	} else if !ok {
		return Revision{}, ErrNotFound
	}
	row, err := revisionByNumber(ctx, tx, incidentID, n)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, ErrRevisionNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("read revision: %w", err)
	}
	return withFindings(ctx, tx, row)
}
