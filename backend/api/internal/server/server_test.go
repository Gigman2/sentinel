package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/server"
	"github.com/Gigman2/sentinel/backend/api/internal/testutil"
)

func TestHealthz(t *testing.T) {
	pool := testutil.NewDB(t)
	h := server.New(server.Deps{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthy: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID")
	}

	pool.Close()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "database_unavailable") {
		t.Fatalf("db down: %d %s", w.Code, w.Body)
	}
}
