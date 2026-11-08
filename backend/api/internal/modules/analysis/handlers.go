package analysis

import (
	"errors"
	"log/slog"
	"net/http"

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
		h.Log.Error("reanalyze", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
	default:
		httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
	}
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching incident found")
}
