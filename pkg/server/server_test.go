package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		ready ReadyFunc
		want  int
	}{
		{"healthz", "/healthz", nil, http.StatusOK},
		{"readyz without check", "/readyz", nil, http.StatusOK},
		{"readyz ready", "/readyz", func(context.Context) error { return nil }, http.StatusOK},
		{"readyz not ready", "/readyz", func(context.Context) error { return errors.New("db down") }, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			Health(mux, tt.ready)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != tt.want {
				t.Fatalf("GET %s = %d, want %d", tt.path, rec.Code, tt.want)
			}
		})
	}
}
