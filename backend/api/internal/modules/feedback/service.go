// Package feedback records engineers' verdicts on findings and which next
// checks they have done (PRD UI-05). Both belong to a single finding, so they
// stay with its revision.
package feedback

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/modules/analysis"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Verdicts.
const (
	VerdictUseful    = "useful"
	VerdictIncorrect = "incorrect"
)

// ErrNotFound means the finding does not exist or belongs to another organization.
var ErrNotFound = errors.New("finding not found")

var errNotACheck = &httpx.Error{Status: http.StatusBadRequest, Code: "not_a_check", Message: "only next-check findings can be marked done"}

// Service records feedback and check status.
type Service struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

// SetVerdict records userID's verdict on a finding visible to orgID and
// returns the finding's feedback. An empty verdict clears it.
func (s Service) SetVerdict(ctx context.Context, orgID, userID, findingID, verdict string) (analysis.Feedback, error) {
	if verdict != "" && verdict != VerdictUseful && verdict != VerdictIncorrect {
		return analysis.Feedback{}, &httpx.Error{Status: http.StatusBadRequest, Code: "invalid_verdict",
			Message: `verdict must be "useful" or "incorrect"`}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return analysis.Feedback{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := findingKindInOrg(ctx, tx, orgID, findingID); errors.Is(err, pgx.ErrNoRows) {
		return analysis.Feedback{}, ErrNotFound
	} else if err != nil {
		return analysis.Feedback{}, err
	}
	if verdict == "" {
		err = deleteVerdict(ctx, tx, findingID, userID)
	} else {
		err = upsertVerdict(ctx, tx, findingID, userID, verdict, s.Now().UTC())
	}
	if err != nil {
		return analysis.Feedback{}, err
	}
	fb, err := analysis.FeedbackFor(ctx, tx, findingID, userID)
	if err != nil {
		return analysis.Feedback{}, err
	}
	return fb, tx.Commit(ctx)
}

// SetDone marks a next-check finding visible to orgID as done by userID, or
// not done. Marking an already-done check keeps whoever did it first.
func (s Service) SetDone(ctx context.Context, orgID, userID, findingID string, done bool) (analysis.Check, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return analysis.Check{}, err
	}
	defer tx.Rollback(ctx)

	kind, err := findingKindInOrg(ctx, tx, orgID, findingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return analysis.Check{}, ErrNotFound
	}
	if err != nil {
		return analysis.Check{}, err
	}
	if kind != "next_check" {
		return analysis.Check{}, errNotACheck
	}
	if done {
		err = markDone(ctx, tx, findingID, userID, s.Now().UTC())
	} else {
		err = markNotDone(ctx, tx, findingID)
	}
	if err != nil {
		return analysis.Check{}, err
	}
	doneAt, doneBy, byName, err := checkStatus(ctx, tx, findingID)
	if err != nil {
		return analysis.Check{}, err
	}
	return *analysis.CheckFrom(doneAt, doneBy, byName), tx.Commit(ctx)
}
