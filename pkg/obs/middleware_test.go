package obs

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChainRequestIDAndRecover(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(RequestID(r.Context())))
	})
	mux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := Chain(log, mux)

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "abc-123" || rec.Header().Get(RequestIDHeader) != "abc-123" {
		t.Fatalf("request ID not propagated: body %q header %q", rec.Body.String(), rec.Header().Get(RequestIDHeader))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic returned %d", rec.Code)
	}
	if rec.Header().Get(RequestIDHeader) == "" {
		t.Fatal("generated request ID missing")
	}
	logs := buf.String()
	if !strings.Contains(logs, `"stack"`) || strings.Count(logs, `"request_id"`) < 3 {
		t.Fatalf("logs missing stack or request IDs:\n%s", logs)
	}
}
