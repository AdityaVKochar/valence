package httpblob

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/blob/memblob"
)

func TestReadsThroughTheInternalEndpoint(t *testing.T) {
	backing := memblob.New()
	ctx := context.Background()
	key, _, err := backing.Put(ctx, strings.NewReader("1 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		rc, err := backing.Get(r.Context(), blob.Key(strings.TrimPrefix(r.URL.Path, "/internal/blobs/")))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer rc.Close()
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, rc)
		}
	}))
	defer srv.Close()

	s := New(srv.URL+"/", "tok", nil)
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "1 2\n" {
		t.Fatalf("got %q", b)
	}
	if ok, err := s.Exists(ctx, key); !ok || err != nil {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	missing := blob.Key(strings.Repeat("0", 64))
	if _, err := s.Get(ctx, missing); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
	if ok, err := s.Exists(ctx, missing); ok || err != nil {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}
	if _, err := New(srv.URL, "wrong", nil).Get(ctx, key); err == nil || errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, _, err := s.Put(ctx, strings.NewReader("x")); !errors.Is(err, blob.ErrNotSupported) {
		t.Fatalf("Put: %v", err)
	}
}
