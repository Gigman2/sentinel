// Package httpx holds Sentinel's HTTP conventions: JSON responses, the error
// envelope, request IDs and strict request-body decoding.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Error is a client-facing error: an HTTP status, a stable machine-readable
// code, and a message that is safe to show (never secrets or internals).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// WriteError writes the standard error envelope:
// {"error":{"code":...,"message":...},"request_id":...}
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	var b errorBody
	b.Error.Code = code
	b.Error.Message = message
	b.RequestID = RequestIDFrom(r.Context())
	WriteJSON(w, status, b)
}

// WriteErr writes e using WriteError.
func WriteErr(w http.ResponseWriter, r *http.Request, e *Error) {
	WriteError(w, r, e.Status, e.Code, e.Message)
}

type requestIDKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// RequestID keeps a well-formed inbound X-Request-ID or generates one, stores
// it in the request context and echoes it on the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom returns the request ID stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("httpx: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a canonical UUID string.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// DecodeJSON decodes exactly one JSON value from the body into dst. It rejects
// bodies over maxBytes (413 payload_too_large), unknown fields (named in the
// message), trailing data, and malformed JSON (400 invalid_json). It returns
// nil on success; the caller writes any returned error with WriteErr.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) *Error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	// Exactly one value: anything after it, even a stray brace, is rejected.
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return decodeError(err)
		}
		return &Error{Status: http.StatusBadRequest, Code: "invalid_json", Message: "request body must contain a single JSON object"}
	}
	return nil
}

func decodeError(err error) *Error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &Error{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "request body is too large"}
	}
	msg := "request body is not valid JSON"
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		msg = "unknown field " + field
	} else if errors.Is(err, io.EOF) {
		msg = "request body is empty"
	} else {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" {
			msg = "field " + typeErr.Field + " has the wrong type"
		}
	}
	return &Error{Status: http.StatusBadRequest, Code: "invalid_json", Message: msg}
}
