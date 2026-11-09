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
		h.unavailable(w, r, "get incident", err)
	default:
		httpx.WriteJSON(w, http.StatusOK, inc)
	}
}

// Resolve handles POST /v1/incidents/{incidentId}/resolve. Any member may resolve.
func (h Handlers) Resolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incidentId")
	if !httpx.IsUUID(id) {
		writeNotFound(w, r)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if rej := httpx.DecodeJSON(w, r, &body, maxBodyBytes); rej != nil {
		httpx.WriteErr(w, r, rej)
		return
	}
	p := auth.PrincipalFrom(r.Context())
	inc, err := h.Service.Resolve(r.Context(), p.OrganizationID, p.UserID, id, body.Note)
	var rej *httpx.Error
	switch {
	case errors.As(err, &rej):
		httpx.WriteErr(w, r, rej)
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case errors.Is(err, ErrAlreadyResolved):
		httpx.WriteError(w, r, http.StatusConflict, "already_resolved", "this incident is already resolved")
	case err != nil:
		h.unavailable(w, r, "resolve incident", err)
	default:
		httpx.WriteJSON(w, http.StatusOK, inc)
	}
}

// List handles GET /v1/projects/{projectId}/incidents.
func (h Handlers) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.List(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID,
		r.PathValue("projectId"), ListQuery{
			Status: q.Get("status"), Severity: q.Get("severity"), Component: q.Get("component"),
			Since: q.Get("since"), Q: q.Get("q"), Limit: q.Get("limit"), Cursor: q.Get("cursor"),
		})
	h.respondProject(w, r, "list incidents", page, err)
}

// Signals handles GET /v1/projects/{projectId}/signals.
func (h Handlers) Signals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.Signals(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID,
		r.PathValue("projectId"), q.Get("limit"), q.Get("cursor"))
	h.respondProject(w, r, "list signals", page, err)
}

// respondProject writes the result of a project-scoped read.
func (h Handlers) respondProject(w http.ResponseWriter, r *http.Request, what string, page any, err error) {
	var rej *httpx.Error
	switch {
	case errors.As(err, &rej):
		httpx.WriteErr(w, r, rej)
	case errors.Is(err, ErrProjectNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching project found")
	case err != nil:
		h.unavailable(w, r, what, err)
	default:
		httpx.WriteJSON(w, http.StatusOK, page)
	}
}

// Timeline handles GET /v1/incidents/{incidentId}/timeline.
func (h Handlers) Timeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incidentId")
	if !httpx.IsUUID(id) {
		writeNotFound(w, r)
		return
	}
	q := r.URL.Query()
	page, err := h.Service.Timeline(r.Context(), auth.PrincipalFrom(r.Context()).OrganizationID, id, q.Get("limit"), q.Get("cursor"))
	var rej *httpx.Error
	switch {
	case errors.As(err, &rej):
		httpx.WriteErr(w, r, rej)
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case err != nil:
		h.unavailable(w, r, "incident timeline", err)
	default:
		httpx.WriteJSON(w, http.StatusOK, page)
	}
}

// maxBodyBytes bounds request bodies for incident endpoints.
const maxBodyBytes = 64 << 10

func (h Handlers) unavailable(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.Log.Error(what, "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
	httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching incident found")
}
