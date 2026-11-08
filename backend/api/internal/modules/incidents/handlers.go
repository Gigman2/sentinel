package incidents

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Handlers serves the incident endpoints. They must run behind auth.RequireUser.
type Handlers struct {
	Service Service
	Log     *slog.Logger
}

// Get handles GET /v1/incidents/{incidentId}.
func (h Handlers) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incidentId")
	if !httpx.IsUUID(id) {
		writeNotFound(w, r)
		return
	}
	inc, err := h.Service.Get(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case err != nil:
		h.Log.Error("get incident", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
	default:
		httpx.WriteJSON(w, http.StatusOK, inc)
	}
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching incident found")
}
