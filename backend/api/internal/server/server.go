// Package server wires Sentinel's HTTP routes.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/incidents"
	"github.com/Gigman2/sentinel/backend/api/internal/modules/ingest"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Deps are the server's dependencies.
type Deps struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Now    func() time.Time

	// Ingest rate limit per integration. Zero values mean 20 req/s, burst 40.
	IngestRate  rate.Limit
	IngestBurst int
}

// Default ingest rate limit per integration.
const (
	DefaultIngestRate  rate.Limit = 20
	DefaultIngestBurst            = 40
)

// New returns the HTTP handler with all routes.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.IngestRate == 0 {
		d.IngestRate = DefaultIngestRate
	}
	if d.IngestBurst == 0 {
		d.IngestBurst = DefaultIngestBurst
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Pool.Ping(r.Context()); err != nil {
			d.Logger.Error("health check failed", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
			httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the database is unavailable")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	user := auth.RequireUser(d.Pool, d.Logger)

	// Integrations authenticate with their own credential, not a user token.
	mux.Handle("POST /v1/ingest/{integrationId}/events", ingest.Handler{
		Service: ingest.Service{Pool: d.Pool, Now: d.Now},
		Log:     d.Logger,
	})

	inc := incidents.Handlers{Service: incidents.Service{DB: d.Pool}, Log: d.Logger}
	mux.Handle("GET /v1/incidents/{incidentId}", user(http.HandlerFunc(inc.Get)))

	return httpx.RequestID(mux)
}
