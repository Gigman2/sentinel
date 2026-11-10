package analysis

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Handlers serves the analysis endpoints. They must run behind auth.RequireUser.
type Handlers struct {
	Service Service
	Log     *slog.Logger
}

// Reanalyze handles POST /v1/incidents/{incidentId}/reanalyze. Any member
// may request it; the response is 202 with the pending job's ID.
func (h Handlers) Reanalyze(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incidentId")
	if !httpx.IsUUID(id) {
		writeNotFound(w, r)
		return
	}
	jobID, err := h.Service.Reanalyze(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case err != nil:
		h.unavailable(w, r, "reanalyze", err)
	default:
		httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
	}
}

// Revisions handles GET /v1/incidents/{incidentId}/revisions: every
// revision, newest first, with what changed. Revisions per incident are few,
// so the list is not paged.
func (h Handlers) Revisions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incidentId")
	if !httpx.IsUUID(id) {
		writeNotFound(w, r)
		return
	}
	items, err := h.Service.History(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case err != nil:
		h.unavailable(w, r, "revision history", err)
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// Revision handles GET /v1/incidents/{incidentId}/revisions/{n}.
func (h Handlers) Revision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incidentId")
	// revision_number is a 32-bit integer; anything larger cannot exist.
	n, convErr := strconv.ParseInt(r.PathValue("n"), 10, 32)
	if !httpx.IsUUID(id) || convErr != nil || n < 1 {
		writeNotFound(w, r)
		return
	}
	rev, err := h.Service.RevisionByNumber(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID, id, int(n))
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case errors.Is(err, ErrRevisionNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching revision found")
	case err != nil:
		h.unavailable(w, r, "revision", err)
	default:
		httpx.WriteJSON(w, http.StatusOK, rev)
	}
}

func (h Handlers) unavailable(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.Log.Error(what, "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
	httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching incident found")
}
