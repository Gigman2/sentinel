package ingest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// Handler serves POST /v1/ingest/{integrationId}/events.
//
// Checks run in a fixed order: authenticate (401) → enabled (403) → rate
// limit (429) → body size and JSON (413/400) → payload shape (400) → replay
// (200) → timestamps (400) → capability (403) → mapping (422) → store and
// apply in one transaction (201, or 200 if a concurrent duplicate won).
type Handler struct {
	Service Service
	Limiter *Limiter
	Log     *slog.Logger
	// Outcome, if set, is called once per request with its outcome.
	Outcome func(outcome string)
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	integrationID := r.PathValue("integrationId")

	// One log line per request. Never the body or the Authorization header.
	var outcome, eventID string
	defer func() {
		h.Log.Info("ingest", "request_id", httpx.RequestIDFrom(ctx), "integration_id", integrationID,
			"event_id", eventID, "outcome", outcome)
		if h.Outcome != nil {
			h.Outcome(outcome)
		}
	}()
	fail := func(e *httpx.Error) {
		outcome = "rejected_" + e.Code
		httpx.WriteErr(w, r, e)
	}
	internal := func(err error) {
		outcome = "error"
		h.Log.Error("ingest failed", "err", err, "request_id", httpx.RequestIDFrom(ctx), "integration_id", integrationID)
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
	}

	// Authenticate before reading the body. Every credential failure looks
	// the same, so callers cannot probe which integration IDs exist, and
	// nothing is stored for unauthenticated requests.
	token, ok := auth.BearerToken(r)
	if !ok || !httpx.IsUUID(integrationID) {
		fail(invalidCredential)
		return
	}
	in, err := h.Service.Authenticate(ctx, integrationID, token)
	if errors.Is(err, ErrInvalidCredential) {
		fail(invalidCredential)
		return
	}
	if err != nil {
		internal(err)
		return
	}
	if !in.Enabled {
		fail(&httpx.Error{Status: http.StatusForbidden, Code: "integration_disabled", Message: "this integration is disabled"})
		return
	}
	if !h.Limiter.Allow(in.ID) {
		w.Header().Set("Retry-After", "1")
		fail(&httpx.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many events from this integration; retry shortly"})
		return
	}

	var env Envelope
	if rej := httpx.DecodeJSON(w, r, &env, MaxBodyBytes); rej != nil {
		fail(rej)
		return
	}
	if rej := ValidateShape(env); rej != nil {
		fail(rej)
		return
	}

	// A resend is answered from storage before anything else is checked or applied.
	if res, found, err := h.Service.Replay(ctx, in, env.SourceEventID); err != nil {
		internal(err)
		return
	} else if found {
		outcome, eventID = "replayed", res.EventID
		httpx.WriteJSON(w, http.StatusOK, res)
		return
	}

	if rej := ValidateTimes(env, h.Service.Now()); rej != nil {
		fail(rej)
		return
	}

	res, err := h.Service.Accept(ctx, in, env)
	var rej *Rejection
	switch {
	case errors.As(err, &rej):
		fail(rej)
	case err != nil:
		internal(err)
	case res.Replayed:
		outcome, eventID = "replayed", res.EventID
		httpx.WriteJSON(w, http.StatusOK, res)
	default:
		outcome, eventID = "accepted", res.EventID
		httpx.WriteJSON(w, http.StatusCreated, res)
	}
}

var invalidCredential = &httpx.Error{
	Status:  http.StatusUnauthorized,
	Code:    "invalid_credential",
	Message: "a valid integration credential is required",
}
