package tenancy

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// MaxBodyBytes caps management request bodies.
const MaxBodyBytes = 1 << 20

// Handlers serves project and component endpoints. They must run behind auth.RequireUser.
type Handlers struct {
	Service Service
	Log     *slog.Logger
}

// CreateProject handles POST /v1/projects.
func (h Handlers) CreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(w, r, &body, MaxBodyBytes); err != nil {
		httpx.WriteErr(w, r, err)
		return
	}
	p, err := h.Service.CreateProject(r.Context(), org(r), body.Slug, body.Name)
	h.respond(w, r, http.StatusCreated, p, err)
}

// ListProjects handles GET /v1/projects.
func (h Handlers) ListProjects(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.ListProjects(r.Context(), org(r))
	h.respond(w, r, http.StatusOK, map[string]any{"items": items}, err)
}

// CreateComponent handles POST /v1/projects/{projectId}/components.
func (h Handlers) CreateComponent(w http.ResponseWriter, r *http.Request) {
	var in ComponentInput
	if err := httpx.DecodeJSON(w, r, &in, MaxBodyBytes); err != nil {
		httpx.WriteErr(w, r, err)
		return
	}
	c, err := h.Service.CreateComponent(r.Context(), org(r), r.PathValue("projectId"), in)
	h.respond(w, r, http.StatusCreated, c, err)
}

// ListComponents handles GET /v1/projects/{projectId}/components.
func (h Handlers) ListComponents(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.ListComponents(r.Context(), org(r), r.PathValue("projectId"))
	h.respond(w, r, http.StatusOK, map[string]any{"items": items}, err)
}

// ListDependencies handles GET /v1/projects/{projectId}/dependencies.
func (h Handlers) ListDependencies(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.ListDependencies(r.Context(), org(r), r.PathValue("projectId"))
	h.respond(w, r, http.StatusOK, map[string]any{"items": items}, err)
}

// AddDependency handles POST /v1/projects/{projectId}/dependencies {from, to}.
func (h Handlers) AddDependency(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := httpx.DecodeJSON(w, r, &body, MaxBodyBytes); err != nil {
		httpx.WriteErr(w, r, err)
		return
	}
	d, err := h.Service.AddDependency(r.Context(), org(r), r.PathValue("projectId"), body.From, body.To)
	h.respond(w, r, http.StatusCreated, d, err)
}

// RemoveDependency handles DELETE /v1/projects/{projectId}/dependencies/{fromId}/{toId}.
func (h Handlers) RemoveDependency(w http.ResponseWriter, r *http.Request) {
	err := h.Service.RemoveDependency(r.Context(), org(r), r.PathValue("projectId"), r.PathValue("fromId"), r.PathValue("toId"))
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.respond(w, r, http.StatusNoContent, nil, err)
}

func org(r *http.Request) string { return auth.PrincipalFrom(r.Context()).OrganizationID }

func (h Handlers) respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	var clientErr *httpx.Error
	switch {
	case err == nil:
		httpx.WriteJSON(w, status, v)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching project found")
	case errors.Is(err, ErrDependencyNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching dependency found")
	case errors.As(err, &clientErr):
		httpx.WriteErr(w, r, clientErr)
	default:
		h.Log.Error("tenancy request failed", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
	}
}
