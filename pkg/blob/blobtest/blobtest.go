package blobtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/AdityaVKochar/valence/pkg/blob"
)

func Run(t *testing.T, newStore func(t *testing.T) blob.Store) {
	t.Run("PutGet", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		content := []byte("1 2\n")
		key, size, err := s.Put(ctx, bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if string(key) != hex.EncodeToString(sum[:]) || size != int64(len(content)) {
			t.Fatalf("Put = %s, %d", key, size)
		}
		if got := read(t, s, key); !bytes.Equal(got, content) {
			t.Fatalf("Get = %q", got)
		}
		ok, err := s.Exists(ctx, key)
		if err != nil || !ok {
			t.Fatalf("Exists = %v, %v", ok, err)
		}
	})

	t.Run("Empty", func(t *testing.T) {
		s := newStore(t)
		key, size, err := s.Put(context.Background(), strings.NewReader(""))
		if err != nil || size != 0 || key != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Fatalf("Put empty = %s, %d, %v", key, size, err)
		}
		if got := read(t, s, key); len(got) != 0 {
			t.Fatalf("Get empty = %q", got)
		}
	})

	t.Run("Idempotent", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		content := bytes.Repeat([]byte("abc"), 100_000)
		var wg sync.WaitGroup
		keys := make([]blob.Key, 8)
		for i := range keys {
			wg.Add(1)
			go func() {
				defer wg.Done()
				k, _, err := s.Put(ctx, bytes.NewReader(content))
				if err != nil {
					t.Error(err)
				}
				keys[i] = k
			}()
		}
		wg.Wait()
		for _, k := range keys {
			if k != keys[0] {
				t.Fatalf("keys differ: %v", keys)
			}
		}
		if got := read(t, s, keys[0]); !bytes.Equal(got, content) {
			t.Fatal("content changed after concurrent puts")
		}
	})

	t.Run("Missing", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for _, k := range []blob.Key{
			"0000000000000000000000000000000000000000000000000000000000000000",
			"../../etc/passwd",
			"",
		} {
			if _, err := s.Get(ctx, k); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("Get(%q) = %v, want ErrNotFound", k, err)
			}
			if ok, err := s.Exists(ctx, k); ok || err != nil {
				t.Errorf("Exists(%q) = %v, %v", k, ok, err)
			}
		}
	})

	t.Run("ReaderError", func(t *testing.T) {
		s := newStore(t)
		if _, _, err := s.Put(context.Background(), io.MultiReader(strings.NewReader("x"), errReader{})); err == nil {
			t.Fatal("Put succeeded despite a reader error")
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func read(t *testing.T, s blob.Store, key blob.Key) []byte {
	t.Helper()
	rc, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
