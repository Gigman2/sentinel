package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

type body struct {
	Name string `json:"name"`
}

func decode(t *testing.T, payload string, max int64) (*body, *httpx.Error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	w := httptest.NewRecorder()
	var b body
	err := httpx.DecodeJSON(w, r, &b, max)
	return &b, err
}

func TestDecodeJSON(t *testing.T) {
	if b, err := decode(t, `{"name":"x"}`, 1024); err != nil || b.Name != "x" {
		t.Fatalf("valid body: got %+v, %v", b, err)
	}

	cases := []struct {
		name, payload string
		max           int64
		status        int
		code          string
		msgContains   string
	}{
		{"unknown field named", `{"name":"x","colour":"red"}`, 1024, 400, "invalid_json", "colour"},
		{"trailing brace", `{"name":"x"}}`, 1024, 400, "invalid_json", ""},
		{"trailing object", `{"name":"x"} {"name":"y"}`, 1024, 400, "invalid_json", ""},
		{"not json", `nope`, 1024, 400, "invalid_json", ""},
		{"empty", ``, 1024, 400, "invalid_json", ""},
		{"too large", `{"name":"` + strings.Repeat("a", 100) + `"}`, 32, 413, "payload_too_large", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decode(t, c.payload, c.max)
			if err == nil {
				t.Fatal("expected an error")
			}
			if err.Status != c.status || err.Code != c.code {
				t.Fatalf("got %d %s, want %d %s", err.Status, err.Code, c.status, c.code)
			}
			if c.msgContains != "" && !strings.Contains(err.Message, c.msgContains) {
				t.Fatalf("message %q should name %q", err.Message, c.msgContains)
			}
		})
	}
}

func TestWriteErrorShapeAndRequestID(t *testing.T) {
	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusTeapot, "some_code", "safe text")
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	id := w.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("missing X-Request-ID")
	}
	var got struct {
		Error     struct{ Code, Message string } `json:"error"`
		RequestID string                         `json:"request_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusTeapot || got.Error.Code != "some_code" || got.Error.Message != "safe text" || got.RequestID != id {
		t.Fatalf("unexpected response %d %+v (header id %s)", w.Code, got, id)
	}

	// A well-formed inbound ID is kept; a malformed one is replaced.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Request-ID", "abc-123")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("inbound id not kept: %q", w.Header().Get("X-Request-ID"))
	}
	r.Header.Set("X-Request-ID", "bad id <script>")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("X-Request-ID"); got == "bad id <script>" || got == "" {
		t.Fatalf("malformed inbound id should be replaced, got %q", got)
	}
}

func TestIsUUID(t *testing.T) {
	if !httpx.IsUUID("2dd80324-2e0e-421e-95f0-f848067c4124") {
		t.Fatal("valid uuid rejected")
	}
	for _, s := range []string{"", "nope", "2dd80324-2e0e-421e-95f0-f848067c412", "2dd80324x2e0e-421e-95f0-f848067c4124"} {
		if httpx.IsUUID(s) {
			t.Fatalf("%q accepted", s)
		}
	}
}
