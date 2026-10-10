package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

const RequestIDHeader = "X-Request-Id"

type requestIDKey struct{}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func AccessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			level = slog.LevelDebug
		}
		log.Log(r.Context(), level, "HTTP request",
			"request_id", RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func Recover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("Handler panicked", "request_id", RequestID(r.Context()), "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func Chain(log *slog.Logger, h http.Handler) http.Handler {
	return WithRequestID(AccessLog(log, Recover(log, h)))
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
