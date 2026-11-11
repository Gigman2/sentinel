// Package demodata fills the seeded demo tenant with incidents that, between
// them, show every part of the engineer view. It is for local stacks only:
// `make run` and `make up` run it after the seed (sentinel-api demo-data),
// unless SEED_DEMO_DATA=0.
//
// Everything goes through the same services the API uses: events are
// validated and accepted by ingest (so grouping, linking and job enqueueing
// are the real ones), incidents are resolved and reanalysed, and feedback is
// recorded, as an engineer would. Steps that need a finished analysis wait
// for the worker.
//
// Event IDs are fixed, so a rerun is answered with replays; components,
// mappings and dependencies that exist are kept; and actions that already
// happened are skipped. Running it again changes nothing.
package demodata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gigman2/sentinel/backend/api/internal/domain"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/analysis"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/feedback"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/incidents"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/ingest"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/integrations"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/tenancy"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

const environment = "production"

// Analysis job statuses (analysis_jobs.status).
const (
	jobSucceeded = "succeeded"
	jobFailed    = "failed"
)

// Config says where to write and how long to wait for the worker.
type Config struct {
	Pool *pgxpool.Pool
	// OrgSlug names the seeded organisation; the data goes into its
	// uppsel-production project, through its monitor and deploys integrations.
	OrgSlug string
	// AdminEmail names the seeded admin, who records the feedback, checks
	// and resolutions.
	AdminEmail string
	// Now is the clock; events are placed relative to it. Default time.Now.
	Now func() time.Time
	// Wait bounds each wait for an analysis. Default 5 minutes.
	Wait time.Duration
	// Poll is how often a wait checks. Default 500 ms.
	Poll time.Duration
	// Log, if set, is told about each finished story, or why nothing was done.
	Log func(format string, args ...any)
}

// ErrWorkerNotRunning means an analysis did not finish within Config.Wait.
var ErrWorkerNotRunning = errors.New("analysis did not finish in time; is the worker running?")

// Run creates the demo data, or confirms it is already there.
func Run(ctx context.Context, c Config) error {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Wait == 0 {
		c.Wait = 5 * time.Minute
	}
	if c.Poll == 0 {
		c.Poll = 500 * time.Millisecond
	}
	if c.Log == nil {
		c.Log = func(string, ...any) {}
	}
	t, err := locate(ctx, c.Pool, c.OrgSlug, c.AdminEmail)
	if err != nil {
		return fmt.Errorf("demo data: %w", err)
	}
	if !t.monitor.Enabled || !t.deploys.Enabled {
		c.Log("demo data: skipped, because the monitor or deploys integration is disabled")
		return nil
	}
	now, err := anchor(ctx, c.Pool, t.deploys.ID, c.Now)
	if err != nil {
		return fmt.Errorf("demo data: %w", err)
	}
	r := &runner{
		Config:  c,
		tenant:  t,
		ingest:  ingest.Service{Pool: c.Pool, Now: c.Now},
		inc:     incidents.Service{Pool: c.Pool, Now: c.Now},
		an:      analysis.Service{Pool: c.Pool},
		fb:      feedback.Service{Pool: c.Pool, Now: c.Now},
		ten:     tenancy.Service{DB: c.Pool},
		integ:   integrations.Service{Pool: c.Pool},
		now:     now,
		retried: map[string]bool{},
	}
	if err := r.setup(ctx); err != nil {
		return fmt.Errorf("demo data setup: %w", err)
	}
	for _, story := range []struct {
		name string
		run  func(context.Context) (string, error)
	}{
		{"payment-service: six days ago, resolved by its monitor", r.paymentsLastWeek},
		{"notification-service: resolved by an engineer with a note", r.notificationsManual},
		{"ledger-service: resolved by its monitor", r.ledgerAutoResolved},
		{"payment-service: late deployment, revision history", r.paymentsLateDeploy},
		{"search-api: evidence gaps, a finding marked incorrect", r.searchQuiet},
		{"checkout-api: escalated, reanalysed, feedback, a check done", r.checkout},
	} {
		incident, err := story.run(ctx)
		if err != nil {
			return fmt.Errorf("demo data, %s: %w", story.name, err)
		}
		r.Log("demo data: %s (incident %s)", story.name, incident)
	}
	return nil
}

type runner struct {
	Config
	tenant
	ingest  ingest.Service
	inc     incidents.Service
	an      analysis.Service
	fb      feedback.Service
	ten     tenancy.Service
	integ   integrations.Service
	now     time.Time
	retried map[string]bool // incidents whose failed analysis was requeued
}

// tenant is where the data goes, read from the database.
type tenant struct {
	orgID, projectID, adminID string
	monitor, deploys          ingest.Integration
}

func locate(ctx context.Context, pool *pgxpool.Pool, orgSlug, adminEmail string) (tenant, error) {
	var t tenant
	err := pool.QueryRow(ctx, `
		SELECT o.id::text, p.id::text, u.id::text
		FROM organizations o
		JOIN projects p ON p.organization_id = o.id AND p.slug = 'uppsel-production'
		JOIN memberships m ON m.organization_id = o.id
		JOIN users u ON u.id = m.user_id AND u.email = $2
		WHERE o.slug = $1`, orgSlug, adminEmail).Scan(&t.orgID, &t.projectID, &t.adminID)
	if err != nil {
		return t, fmt.Errorf("find seeded tenant %s (admin %s): %w; run `sentinel-api seed` first", orgSlug, adminEmail, err)
	}
	for _, x := range []struct {
		name string
		into *ingest.Integration
	}{{"monitor", &t.monitor}, {"deploys", &t.deploys}} {
		in := ingest.Integration{OrganizationID: t.orgID, ProjectID: t.projectID}
		err := pool.QueryRow(ctx, `
			SELECT id::text, capability, enabled FROM integrations
			WHERE project_id = $1 AND name = $2 ORDER BY created_at LIMIT 1`, t.projectID, x.name).
			Scan(&in.ID, &in.Capability, &in.Enabled)
		if err != nil {
			return t, fmt.Errorf("find the %s integration: %w", x.name, err)
		}
		*x.into = in
	}
	return t, nil
}

// firstEvent is the first event Run sends, and how long before the run's
// reference time it is placed.
const (
	firstEvent       = "demo-data-old-deploy"
	firstEventBefore = 6*24*time.Hour + 9*time.Minute
)

// anchor is the reference time every event is placed relative to. A run that
// finds events from an earlier run (one that stopped partway, say) reuses
// that run's reference time, so the events it adds land where the first run
// meant them to, next to the ones already stored.
func anchor(ctx context.Context, pool *pgxpool.Pool, deploysID string, now func() time.Time) (time.Time, error) {
	var first time.Time
	err := pool.QueryRow(ctx, `SELECT occurred_at FROM operational_events WHERE integration_id = $1 AND source_event_id = $2`,
		deploysID, firstEvent).Scan(&first)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return now().UTC().Truncate(time.Second), nil
	case err != nil:
		return time.Time{}, fmt.Errorf("find earlier demo data: %w", err)
	}
	return first.UTC().Add(firstEventBefore), nil
}

// Setup ---------------------------------------------------------------------

var components = []tenancy.ComponentInput{
	{Slug: "ledger-service", DisplayName: "Ledger service", Kind: tenancy.KindInternal},
	{Slug: "notification-service", DisplayName: "Notification service", Kind: tenancy.KindInternal},
	{Slug: "search-api", DisplayName: "Search API", Kind: tenancy.KindInternal},
	{Slug: "bank-gateway", DisplayName: "Bank gateway", Kind: tenancy.KindExternal},
}

// Registered "calls" edges, besides the seeded checkout-api → payment-service
// → payment-provider.
var dependencies = [][2]string{
	{"payment-service", "ledger-service"},
	{"ledger-service", "bank-gateway"},
	{"checkout-api", "notification-service"},
}

// exists reports whether err is the client error with one of codes, meaning
// the thing was already there.
func exists(err error, codes ...string) bool {
	var e *httpx.Error
	if !errors.As(err, &e) {
		return false
	}
	for _, c := range codes {
		if e.Code == c {
			return true
		}
	}
	return false
}

func (r *runner) setup(ctx context.Context) error {
	org, project := r.orgID, r.projectID
	for _, c := range components {
		c.Environment = environment
		if _, err := r.ten.CreateComponent(ctx, org, project, c); err != nil && !exists(err, "component_exists") {
			return fmt.Errorf("component %s: %w", c.Slug, err)
		}
	}
	all, err := r.ten.ListComponents(ctx, org, project)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, c := range all {
		if c.Environment == environment {
			ids[c.Slug] = c.ID
		}
	}
	// Each component is known to both integrations by its own slug.
	for _, slug := range []string{"checkout-api", "payment-service", "ledger-service", "notification-service", "search-api"} {
		for _, integration := range []string{r.monitor.ID, r.deploys.ID} {
			_, err := r.integ.AddMapping(ctx, org, integration, integrations.MappingInput{
				ExternalComponentID: slug, Environment: environment, ComponentID: ids[slug]})
			if err != nil && !exists(err, "mapping_exists") {
				return fmt.Errorf("mapping %s: %w", slug, err)
			}
		}
	}
	for _, d := range dependencies {
		if _, err := r.ten.AddDependency(ctx, org, project, ids[d[0]], ids[d[1]]); err != nil && !exists(err, "duplicate") {
			return fmt.Errorf("dependency %s → %s: %w", d[0], d[1], err)
		}
	}
	return nil
}

// Events ----------------------------------------------------------------------

func (r *runner) ago(d time.Duration) *time.Time {
	t := r.now.Add(-d)
	return &t
}

// send ingests an event through the same pipeline as the webhook.
func (r *runner) send(ctx context.Context, in ingest.Integration, e ingest.Envelope) (ingest.Result, error) {
	e.SchemaVersion, e.Environment = "1", environment
	return r.ingest.Ingest(ctx, in, e)
}

type alert struct {
	key, component, severity, summary, fingerprint string
	ago                                            time.Duration
	resolved                                       bool
}

func (r *runner) alert(ctx context.Context, a alert) (string, error) {
	typ := domain.EventAlertTriggered
	if a.resolved {
		typ = domain.EventAlertResolved
	}
	res, err := r.send(ctx, r.monitor, ingest.Envelope{
		SourceEventID: "demo-data-" + a.key, Type: typ, OccurredAt: r.ago(a.ago),
		ExternalComponentID: a.component, Severity: a.severity, Fingerprint: a.fingerprint, Summary: a.summary,
		SourceURL: "https://monitor.example/alerts/" + a.fingerprint,
	})
	if err != nil {
		return "", fmt.Errorf("alert %s: %w", a.key, err)
	}
	if res.IncidentID == nil {
		return "", nil
	}
	return *res.IncidentID, nil
}

func (r *runner) errorEvent(ctx context.Context, key, component, summary string, ago time.Duration, count int) error {
	_, err := r.send(ctx, r.monitor, ingest.Envelope{
		SourceEventID: "demo-data-" + key, Type: domain.EventErrorObserved, OccurredAt: r.ago(ago),
		ExternalComponentID: component, Severity: "error", Summary: summary,
		Attributes: map[string]any{"count": count},
	})
	if err != nil {
		return fmt.Errorf("error event %s: %w", key, err)
	}
	return nil
}

func (r *runner) deploy(ctx context.Context, key, component, version, sha string, ago time.Duration) error {
	_, err := r.send(ctx, r.deploys, ingest.Envelope{
		SourceEventID: "demo-data-" + key, Type: domain.EventDeploymentCompleted, DeployedAt: r.ago(ago),
		ExternalComponentID: component, Version: version, CommitSHA: sha, Repository: "uppsel/" + component,
		Summary:   fmt.Sprintf("Deploy %s %s", component, version),
		SourceURL: fmt.Sprintf("https://deploys.example/%s/%s", component, version),
	})
	if err != nil {
		return fmt.Errorf("deployment %s: %w", key, err)
	}
	return nil
}

// Analysis and engineer actions ------------------------------------------------

// wait waits until the incident's analysis has succeeded with at least n
// revisions. A failed analysis is requeued once per incident, as an engineer
// would with Reanalyze, so one bad job does not stop the demo data for good.
func (r *runner) wait(ctx context.Context, incident string, n int) (analysis.View, error) {
	deadline := time.Now().Add(r.Wait)
	for {
		v, err := analysis.Read(ctx, r.Pool, incident, r.adminID)
		if err != nil {
			return analysis.View{}, err
		}
		switch {
		case v.Status == jobSucceeded && v.RevisionCount >= n:
			return v, nil
		case v.Status == jobFailed && !r.retried[incident]:
			r.retried[incident] = true
			if _, err := r.an.Reanalyze(ctx, r.orgID, incident); err != nil {
				return v, fmt.Errorf("requeue failed analysis of %s: %w", incident, err)
			}
		case v.Status == jobFailed:
			msg := ""
			if v.LastError != nil {
				msg = *v.LastError
			}
			return v, fmt.Errorf("analysis of %s failed again: %s", incident, msg)
		case time.Now().After(deadline):
			return v, ErrWorkerNotRunning
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(r.Poll):
		}
	}
}

func finding(v analysis.View, ruleID string) *analysis.Finding {
	if v.LatestRevision == nil {
		return nil
	}
	for i := range v.LatestRevision.Findings {
		if v.LatestRevision.Findings[i].RuleID == ruleID {
			return &v.LatestRevision.Findings[i]
		}
	}
	return nil
}

func (r *runner) verdict(ctx context.Context, v analysis.View, ruleID, verdict string) error {
	f := finding(v, ruleID)
	if f == nil {
		return nil
	}
	_, err := r.fb.SetVerdict(ctx, r.orgID, r.adminID, f.ID, verdict)
	return err
}

// Stories -----------------------------------------------------------------------

func (r *runner) paymentsLastWeek(ctx context.Context) (string, error) {
	const day = 24 * time.Hour
	if err := r.deploy(ctx, "old-deploy", "payment-service", "1.8.0", "77aa310", 6*day+9*time.Minute); err != nil {
		return "", err
	}
	incident, err := r.alert(ctx, alert{key: "old-alert", component: "payment-service", severity: "critical",
		summary: "Payment provider timeouts", fingerprint: "payment-provider-timeouts", ago: 6 * day})
	if err != nil {
		return "", err
	}
	if _, err := r.wait(ctx, incident, 1); err != nil {
		return incident, err
	}
	_, err = r.alert(ctx, alert{key: "old-resolved", component: "payment-service", severity: "critical",
		summary: "Payment provider timeouts recovered", fingerprint: "payment-provider-timeouts",
		ago: 6*day - 50*time.Minute, resolved: true})
	return incident, err
}

func (r *runner) notificationsManual(ctx context.Context) (string, error) {
	incident, err := r.alert(ctx, alert{key: "notify-alert", component: "notification-service", severity: "warning",
		summary: "Order emails delayed > 10 min", fingerprint: "notify-delay", ago: 48 * time.Hour})
	if err != nil {
		return "", err
	}
	if _, err := r.wait(ctx, incident, 1); err != nil {
		return incident, err
	}
	_, err = r.inc.Resolve(ctx, r.orgID, r.adminID, incident,
		"SMTP provider throttled us after the sender limit was lowered in their console. "+
			"Limit restored; queue drained by 14:20.")
	if errors.Is(err, incidents.ErrAlreadyResolved) {
		err = nil
	}
	return incident, err
}

func (r *runner) ledgerAutoResolved(ctx context.Context) (string, error) {
	if err := r.deploy(ctx, "ledger-deploy", "ledger-service", "4.2.1", "e07f5b3", 26*time.Hour+12*time.Minute); err != nil {
		return "", err
	}
	if err := r.errorEvent(ctx, "ledger-err", "ledger-service", "DeadlockDetected on postings", 26*time.Hour+3*time.Minute, 9); err != nil {
		return "", err
	}
	incident, err := r.alert(ctx, alert{key: "ledger-alert", component: "ledger-service", severity: "error",
		summary: "Ledger posting latency p99 > 2s", fingerprint: "ledger-latency", ago: 26 * time.Hour})
	if err != nil {
		return "", err
	}
	if _, err := r.wait(ctx, incident, 1); err != nil {
		return incident, err
	}
	_, err = r.alert(ctx, alert{key: "ledger-resolved", component: "ledger-service", severity: "error",
		summary: "Ledger posting latency recovered", fingerprint: "ledger-latency",
		ago: 25*time.Hour + 20*time.Minute, resolved: true})
	return incident, err
}

func (r *runner) paymentsLateDeploy(ctx context.Context) (string, error) {
	incident, err := r.alert(ctx, alert{key: "payments-alert", component: "payment-service", severity: "error",
		summary: "Payment authorisation failures", fingerprint: "payment-auth-failures", ago: 3 * time.Hour})
	if err != nil {
		return "", err
	}
	// The deployment arrives only after the first analysis, so it is late
	// evidence and produces revision 2.
	if _, err := r.wait(ctx, incident, 1); err != nil {
		return incident, err
	}
	if err := r.deploy(ctx, "payments-deploy", "payment-service", "1.9.0", "c41e0aa", 3*time.Hour+8*time.Minute); err != nil {
		return incident, err
	}
	_, err = r.wait(ctx, incident, 2)
	return incident, err
}

func (r *runner) searchQuiet(ctx context.Context) (string, error) {
	incident, err := r.alert(ctx, alert{key: "search-alert", component: "search-api", severity: "info",
		summary: "Search index lag above 5 minutes", fingerprint: "search-index-lag", ago: 12 * time.Minute})
	if err != nil {
		return "", err
	}
	v, err := r.wait(ctx, incident, 1)
	if err != nil {
		return incident, err
	}
	if err := r.verdict(ctx, v, "no_error_evidence", feedback.VerdictIncorrect); err != nil {
		return incident, err
	}
	// A resolution for an alert that never opened an incident.
	_, err = r.alert(ctx, alert{key: "search-stale-resolved", component: "search-api", severity: "warning",
		summary: "Search stale results recovered", fingerprint: "search-stale-results", ago: 5 * time.Minute, resolved: true})
	return incident, err
}

func (r *runner) checkout(ctx context.Context) (string, error) {
	// Each step waits for the previous analysis, so the revisions are the
	// same on every run: 1 from the first alert, 2 from the escalating
	// repeat, 3 from a manual reanalysis. Feedback belongs to one revision,
	// so it goes on the latest, the one the UI shows.
	if err := r.deploy(ctx, "checkout-deploy", "checkout-api", "2.14.0", "9b2d71e", 47*time.Minute); err != nil {
		return "", err
	}
	if err := r.errorEvent(ctx, "checkout-err-1", "checkout-api", "CartTotalMismatch", 42*time.Minute, 17); err != nil {
		return "", err
	}
	if err := r.errorEvent(ctx, "checkout-err-2", "checkout-api", "UpstreamTimeout: payment-service", 41*time.Minute, 64); err != nil {
		return "", err
	}
	incident, err := r.alert(ctx, alert{key: "checkout-alert-1", component: "checkout-api", severity: "warning",
		summary: "Checkout error rate above 2%", fingerprint: "checkout-error-rate", ago: 40 * time.Minute})
	if err != nil {
		return "", err
	}
	if _, err := r.wait(ctx, incident, 1); err != nil {
		return incident, err
	}
	if _, err := r.alert(ctx, alert{key: "checkout-alert-2", component: "checkout-api", severity: "critical",
		summary: "Checkout error rate above 10%", fingerprint: "checkout-error-rate", ago: 35 * time.Minute}); err != nil {
		return incident, err
	}
	v, err := r.wait(ctx, incident, 2)
	if err != nil {
		return incident, err
	}
	if v.RevisionCount == 2 { // not yet reanalysed; a rerun finds 3
		if _, err := r.an.Reanalyze(ctx, r.orgID, incident); err != nil {
			return incident, err
		}
	}
	if v, err = r.wait(ctx, incident, 3); err != nil {
		return incident, err
	}
	if err := r.verdict(ctx, v, "deployment_near_onset", feedback.VerdictUseful); err != nil {
		return incident, err
	}
	if f := finding(v, "compare_by_version"); f != nil {
		if _, err := r.fb.SetDone(ctx, r.orgID, r.adminID, f.ID, true); err != nil {
			return incident, err
		}
	}
	return incident, nil
}
