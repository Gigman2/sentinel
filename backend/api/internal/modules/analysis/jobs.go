// Package analysis owns durable analysis jobs and the analysis read model.
// The Python worker leases jobs and writes revisions; this package enqueues
// them and reads the results.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// Reasons a job is enqueued.
const (
	ReasonIncidentCreated = "incident_created"
	ReasonRepeatTrigger   = "repeat_trigger"
	ReasonLateEvent       = "late_event"
	ReasonManualRetry     = "manual_retry"
)

// StatusNone is reported when an incident has never had a job.
const StatusNone = "none"

// enqueueAttempts bounds the insert-or-lock loop in Enqueue.
const enqueueAttempts = 3

// ErrNotFound means the incident does not exist or belongs to another organization.
var ErrNotFound = errors.New("incident not found")

var errEnqueueContention = errors.New("pending analysis job was leased concurrently on every attempt")

// Enqueue makes sure the incident has a pending analysis job, inside the
// caller's transaction, and returns its ID. New work coalesces into an
// existing pending job (enforced by uq_jobs_one_pending).
//
// The pending job is row-locked, so a worker's lease (FOR UPDATE SKIP LOCKED)
// cannot take it until the caller commits; the worker then sees everything
// the caller wrote. If a worker leases the job between the INSERT and the
// lock, the lock finds nothing and the pair is retried: work must never be
// folded into a job that is already running.
//
// Lock order: callers take the component advisory lock and incident row
// lock first; job rows come last.
func Enqueue(ctx context.Context, tx pgx.Tx, incidentID, reason string) (jobID string, err error) {
	for range enqueueAttempts {
		id, inserted, err := insertPendingJobIfAbsent(ctx, tx, incidentID, reason)
		if err != nil {
			return "", fmt.Errorf("enqueue analysis: %w", err)
		}
		if inserted {
			return id, nil
		}
		id, err = lockPendingJob(ctx, tx, incidentID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // leased concurrently; enqueue a new job
		}
		if err != nil {
			return "", fmt.Errorf("lock pending analysis job: %w", err)
		}
		return id, nil
	}
	return "", errEnqueueContention
}

// Service handles manual reanalysis.
type Service struct {
	Pool *pgxpool.Pool
}

// Reanalyze enqueues a manual_retry job for an incident visible to orgID, or
// returns the job already pending.
func (s Service) Reanalyze(ctx context.Context, orgID, incidentID string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	ok, err := incidentExistsInOrg(ctx, tx, orgID, incidentID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	id, err := Enqueue(ctx, tx, incidentID, ReasonManualRetry)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// View is an incident's analysis state as the engineer sees it.
type View struct {
	Status    string  `json:"status"`
	LastError *string `json:"last_error"`
	// StartedAt and FinishedAt are the newest job's first lease and terminal
	// transition; null when it has not started or finished, or when it
	// predates their recording.
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	// Step is the newest job's current step, or the one it failed at:
	// collect_evidence, draft_findings or save_revision. Null before it starts.
	Step           *string    `json:"step"`
	StepStartedAt  *time.Time `json:"step_started_at"`
	RevisionCount  int        `json:"revision_count"`
	LatestRevision *Revision  `json:"latest_revision"`
}

// Revision is one immutable analysis result.
type Revision struct {
	ID             string    `json:"id"`
	RevisionNumber int       `json:"revision_number"`
	CreatedAt      time.Time `json:"created_at"`
	// Summary is built by the worker only from Findings; empty for
	// revisions written before summaries existed.
	Summary  []Segment `json:"summary"`
	Findings []Finding `json:"findings"`
}

// Segment is one run of summary text. Kind is "text", "time" (an HH:MM UTC
// time that appears in a finding) or "strong".
type Segment struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// Finding is one statement in a revision, with the events it cites.
type Finding struct {
	Kind        string   `json:"kind"`
	RuleID      string   `json:"rule_id"`
	Text        string   `json:"text"`
	Limitations *string  `json:"limitations"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// Read returns the incident's analysis view. Status and last error come from
// the newest job; the revision is the highest-numbered one. Callers that
// show it alongside the incident read both in one snapshot (q is a
// REPEATABLE READ transaction).
func Read(ctx context.Context, q database.DBTX, incidentID string) (View, error) {
	v := View{Status: StatusNone}
	job, err := newestJob(ctx, q, incidentID)
	switch {
	case err == nil:
		v.Status, v.LastError = job.Status, job.LastError
		v.StartedAt, v.FinishedAt = utcPtr(job.StartedAt), utcPtr(job.FinishedAt)
		v.Step, v.StepStartedAt = job.Step, utcPtr(job.StepStartedAt)
	case !errors.Is(err, pgx.ErrNoRows):
		return View{}, fmt.Errorf("read analysis job: %w", err)
	}

	if v.RevisionCount, err = revisionCount(ctx, q, incidentID); err != nil {
		return View{}, fmt.Errorf("count analysis revisions: %w", err)
	}
	rev, err := latestRevision(ctx, q, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return View{}, fmt.Errorf("read analysis revision: %w", err)
	}
	full, err := withFindings(ctx, q, rev)
	if err != nil {
		return View{}, err
	}
	v.LatestRevision = &full
	return v, nil
}

// withFindings loads a revision's findings.
func withFindings(ctx context.Context, q database.DBTX, rev revisionRow) (Revision, error) {
	findings, err := revisionFindings(ctx, q, rev.ID)
	if err != nil {
		return Revision{}, fmt.Errorf("read findings: %w", err)
	}
	if findings == nil {
		findings = []Finding{}
	}
	summary := rev.Summary
	if summary == nil {
		summary = []Segment{}
	}
	return Revision{ID: rev.ID, RevisionNumber: rev.RevisionNumber, CreatedAt: rev.CreatedAt.UTC(), Summary: summary, Findings: findings}, nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
