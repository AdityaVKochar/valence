package testcache

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/blob/memblob"
)

func put(t *testing.T, s blob.Store, content string) blob.Key {
	t.Helper()
	k, _, err := s.Put(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSecondReadHitsCache(t *testing.T) {
	store := memblob.New()
	in, out := put(t, store, "1 2\n"), put(t, store, "3\n")
	c, err := New(t.TempDir(), 1<<20, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		var wg sync.WaitGroup
		for range 4 {
			for _, k := range []blob.Key{in, out} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					p, release, err := c.Acquire(ctx, k)
					if err != nil {
						t.Error(err)
						return
					}
					defer release()
					if _, err := os.Stat(p); err != nil {
						t.Error(err)
					}
				}()
			}
		}
		wg.Wait()
	}
	if store.Gets() != 2 {
		t.Fatalf("store read %d times, want 2 (once per blob)", store.Gets())
	}
}

func TestEvictsLeastRecentlyUsedButNotPinned(t *testing.T) {
	store := memblob.New()
	a := put(t, store, strings.Repeat("a", 400))
	b := put(t, store, strings.Repeat("b", 400))
	d := put(t, store, strings.Repeat("d", 400))
	c, err := New(t.TempDir(), 1000, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pa, releaseA, _ := c.Acquire(ctx, a)
	_, releaseB, _ := c.Acquire(ctx, b)
	releaseB()
	_, releaseD, _ := c.Acquire(ctx, d)
	defer releaseD()
	if _, err := os.Stat(pa); err != nil {
		t.Fatal("pinned blob a was evicted")
	}
	if c.Size() > 1000 {
		t.Fatalf("cache size %d exceeds limit", c.Size())
	}
	c.mu.Lock()
	_, hasB := c.entries[b]
	c.mu.Unlock()
	if hasB {
		t.Fatal("b should have been evicted as least recently used")
	}
	releaseA()
}

func TestReloadsExistingFiles(t *testing.T) {
	store := memblob.New()
	k := put(t, store, "x")
	dir := t.TempDir()
	c1, _ := New(dir, 1<<20, store)
	_, release, err := c1.Acquire(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	release()
	c2, err := New(dir, 1<<20, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, release, err := c2.Acquire(context.Background(), k); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if store.Gets() != 1 {
		t.Fatalf("store read %d times after restart, want 1", store.Gets())
	}
}

func TestRejectsCorruptBlob(t *testing.T) {
	c, _ := New(t.TempDir(), 1<<20, corrupt{memblob.New()})
	if _, _, err := c.Acquire(context.Background(), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"); err == nil {
		t.Fatal("expected a hash mismatch")
	}
}

type corrupt struct{ *memblob.Store }

func (corrupt) Get(context.Context, blob.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("not empty")), nil
}
