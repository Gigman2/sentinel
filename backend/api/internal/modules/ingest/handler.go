package ingest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Handler serves POST /v1/ingest/{integrationId}/events.
type Handler struct {
	Service Service
	Log     *slog.Logger
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Authenticate before reading the body. Every credential failure looks
	// the same, so callers cannot probe which integration IDs exist.
	integrationID := r.PathValue("integrationId")
	token, ok := auth.BearerToken(r)
	if !ok || !httpx.IsUUID(integrationID) {
		writeInvalidCredential(w, r)
		return
	}
	in, err := h.Service.Authenticate(ctx, integrationID, token)
	if errors.Is(err, ErrInvalidCredential) {
		writeInvalidCredential(w, r)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !in.Enabled {
		httpx.WriteError(w, r, http.StatusForbidden, "integration_disabled", "this integration is disabled")
		return
	}

	// 2. Decode and validate.
	var env Envelope
	if rej := httpx.DecodeJSON(w, r, &env, MaxBodyBytes); rej != nil {
		httpx.WriteErr(w, r, rej)
		return
	}
	if rej := Validate(env, h.Service.Now()); rej != nil {
		httpx.WriteErr(w, r, rej)
		return
	}

	// 3. Map, store and apply in one transaction; respond only after commit.
	res, err := h.Service.Accept(ctx, in, env)
	var rej *Rejection
	switch {
	case errors.As(err, &rej):
		httpx.WriteErr(w, r, rej)
	case err != nil:
		h.internalError(w, r, err)
	default:
		httpx.WriteJSON(w, http.StatusCreated, res)
	}
}

func writeInvalidCredential(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_credential", "a valid integration credential is required")
}

func (h Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.Log.Error("ingest failed", "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
	httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
}
