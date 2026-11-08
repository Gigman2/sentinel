package testutil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PostJSON sends body as JSON to h with an optional bearer token.
func PostJSON(t *testing.T, h http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if s, ok := body.(string); ok {
		buf.WriteString(s)
	} else if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return Do(t, h, http.MethodPost, path, token, &buf)
}

// GetJSON sends a GET to h with an optional bearer token.
func GetJSON(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	return Do(t, h, http.MethodGet, path, token, nil)
}

// Do sends a request to h with an optional bearer token.
func Do(t *testing.T, h http.Handler, method, path, token string, body *bytes.Buffer) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Decode unmarshals a JSON response body into T, failing the test on error.
func Decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return v
}

// ErrorBody is the standard error envelope.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// ErrorCode returns the error code from a standard error response.
func ErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return Decode[ErrorBody](t, w).Error.Code
}
