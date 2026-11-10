// Package metrics exposes Sentinel's Prometheus metrics: ingest outcomes,
// counted as they happen, and analysis-job gauges read from the database
// at scrape time.
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics records events for scraping.
type Metrics struct {
	ingest *prometheus.CounterVec
}

// New returns the metrics and the /metrics handler, on a registry of their
// own so each server (and test) starts from zero.
func New(pool *pgxpool.Pool) (*Metrics, http.Handler) {
	reg := prometheus.NewRegistry()
	m := &Metrics{ingest: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sentinel_ingest_events_total",
		Help: "Ingest requests by outcome: accepted, replayed, rejected_<code> or error.",
	}, []string{"outcome"})}
	reg.MustRegister(m.ingest, jobsCollector{pool: pool})
	// A failed gauge query drops only the gauges; the counters are still served.
	return m, promhttp.HandlerFor(reg, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError})
}

// IngestOutcome counts one ingest request. Safe on a nil *Metrics.
func (m *Metrics) IngestOutcome(outcome string) {
	if m != nil && outcome != "" {
		m.ingest.WithLabelValues(outcome).Inc()
	}
}

var (
	jobsDesc = prometheus.NewDesc("sentinel_analysis_jobs",
		"Analysis jobs by status.", []string{"status"}, nil)
	oldestDesc = prometheus.NewDesc("sentinel_analysis_oldest_pending_seconds",
		"Age of the oldest pending analysis job, 0 when none is pending.", nil, nil)
	jobStatuses = []string{"pending", "running", "succeeded", "failed"}
)

type jobsCollector struct{ pool *pgxpool.Pool }

func (jobsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- jobsDesc
	ch <- oldestDesc
}

func (c jobsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	counts, oldest, err := c.read(ctx)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(jobsDesc, err)
		return
	}
	for _, s := range jobStatuses {
		ch <- prometheus.MustNewConstMetric(jobsDesc, prometheus.GaugeValue, counts[s], s)
	}
	ch <- prometheus.MustNewConstMetric(oldestDesc, prometheus.GaugeValue, oldest)
}

func (c jobsCollector) read(ctx context.Context) (map[string]float64, float64, error) {
	rows, err := c.pool.Query(ctx, `SELECT status, count(*) FROM analysis_jobs GROUP BY status`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	counts := map[string]float64{}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return nil, 0, err
		}
		counts[status] = float64(n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var oldest float64
	err = c.pool.QueryRow(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8
		FROM analysis_jobs WHERE status = 'pending'`).Scan(&oldest)
	return counts, oldest, err
}
