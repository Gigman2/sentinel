package integrations

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// MaxBodyBytes caps management request bodies.
const MaxBodyBytes = 1 << 20

// Handlers serves integration endpoints. All run behind auth.RequireUser;
// the mutating ones also need auth.RequireAdmin (wired in server).
type Handlers struct {
	Service Service
	Log     *slog.Logger
}

// Create handles POST /v1/projects/{projectId}/integrations.
func (h Handlers) Create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpx.DecodeJSON(w, r, &in, MaxBodyBytes); err != nil {
		httpx.WriteErr(w, r, err)
		return
	}
	created, err := h.Service.Create(r.Context(), org(r), r.PathValue("projectId"), in)
	if err == nil {
		w.Header().Set("Cache-Control", "no-store") // the only response carrying this credential
	}
	h.respond(w, r, http.StatusCreated, created, err)
}

// List handles GET /v1/projects/{projectId}/integrations.
func (h Handlers) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.List(r.Context(), org(r), r.PathValue("projectId"))
	h.respond(w, r, http.StatusOK, map[string]any{"items": items}, err)
}

// Patch handles PATCH /v1/integrations/{integrationId}.
func (h Handlers) Patch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(w, r, &body, MaxBodyBytes); err != nil {
		httpx.WriteErr(w, r, err)
		return
	}
	if body.Enabled == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing_field", "missing required field enabled")
		return
	}
	in, err := h.Service.SetEnabled(r.Context(), org(r), r.PathValue("integrationId"), *body.Enabled)
	h.respond(w, r, http.StatusOK, in, err)
}

// RotateCredential handles POST /v1/integrations/{integrationId}/rotate-credential.
func (h Handlers) RotateCredential(w http.ResponseWriter, r *http.Request) {
	rotated, err := h.Service.RotateCredential(r.Context(), org(r), r.PathValue("integrationId"))
	if err == nil {
		w.Header().Set("Cache-Control", "no-store")
	}
	h.respond(w, r, http.StatusOK, rotated, err)
}

// AddMapping handles POST /v1/integrations/{integrationId}/mappings.
func (h Handlers) AddMapping(w http.ResponseWriter, r *http.Request) {
	var m MappingInput
	if err := httpx.DecodeJSON(w, r, &m, MaxBodyBytes); err != nil {
		httpx.WriteErr(w, r, err)
		return
	}
	added, err := h.Service.AddMapping(r.Context(), org(r), r.PathValue("integrationId"), m)
	h.respond(w, r, http.StatusCreated, added, err)
}

func org(r *http.Request) string { return auth.PrincipalFrom(r.Context()).OrganizationID }

func (h Handlers) respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	var clientErr *httpx.Error
	switch {
	case err == nil:
		httpx.WriteJSON(w, status, v)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching integration or project found")
	case errors.As(err, &clientErr):
		httpx.WriteErr(w, r, clientErr)
	default:
		h.Log.Error("integrations request failed", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
	}
}
