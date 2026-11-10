package feedback

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

const maxBodyBytes = 4 << 10

// Handlers serves the feedback endpoints. They must run behind auth.RequireUser.
type Handlers struct {
	Service Service
	Log     *slog.Logger
}

// PutFeedback handles PUT /v1/findings/{findingId}/feedback {verdict}.
func (h Handlers) PutFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Verdict string `json:"verdict"`
	}
	if !h.decode(w, r, &body) {
		return
	}
	if body.Verdict == "" { // clearing is DELETE; an empty PUT is a mistake
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_verdict", `verdict must be "useful" or "incorrect"`)
		return
	}
	p := auth.PrincipalFrom(r.Context())
	fb, err := h.Service.SetVerdict(r.Context(), p.OrganizationID, p.UserID, r.PathValue("findingId"), body.Verdict)
	h.respond(w, r, "set feedback", http.StatusOK, fb, err)
}

// DeleteFeedback handles DELETE /v1/findings/{findingId}/feedback. It
// answers 200 with the finding's feedback, like PUT, so the client shows
// everyone's current counts rather than its own estimate.
func (h Handlers) DeleteFeedback(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsUUID(r.PathValue("findingId")) {
		writeNotFound(w, r)
		return
	}
	p := auth.PrincipalFrom(r.Context())
	fb, err := h.Service.SetVerdict(r.Context(), p.OrganizationID, p.UserID, r.PathValue("findingId"), "")
	h.respond(w, r, "clear feedback", http.StatusOK, fb, err)
}

// PutCheck handles PUT /v1/findings/{findingId}/check {done}.
func (h Handlers) PutCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Done *bool `json:"done"`
	}
	if !h.decode(w, r, &body) {
		return
	}
	if body.Done == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing_field", "done is required")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	c, err := h.Service.SetDone(r.Context(), p.OrganizationID, p.UserID, r.PathValue("findingId"), *body.Done)
	h.respond(w, r, "set check", http.StatusOK, c, err)
}

// decode checks the path ID and reads the JSON body; false means a response
// was written.
func (h Handlers) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !httpx.IsUUID(r.PathValue("findingId")) {
		writeNotFound(w, r)
		return false
	}
	if rej := httpx.DecodeJSON(w, r, dst, maxBodyBytes); rej != nil {
		httpx.WriteErr(w, r, rej)
		return false
	}
	return true
}

func (h Handlers) respond(w http.ResponseWriter, r *http.Request, what string, status int, v any, err error) {
	var rej *httpx.Error
	switch {
	case errors.As(err, &rej):
		httpx.WriteErr(w, r, rej)
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r)
	case err != nil:
		h.Log.Error(what, "err", err, "request_id", httpx.RequestIDFrom(r.Context()))
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "the service is temporarily unavailable")
	default:
		httpx.WriteJSON(w, status, v)
	}
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, "not_found", "no matching finding found")
}
