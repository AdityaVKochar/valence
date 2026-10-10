package testcache

import (
	"cmp"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/AdityaVKochar/valence/pkg/blob"
)

type entry struct {
	key  blob.Key
	size int64
	pins int
	elem *list.Element
}

type Cache struct {
	dir      string
	maxBytes int64
	store    blob.Store
	group    singleflight.Group

	mu      sync.Mutex
	entries map[blob.Key]*entry
	lru     *list.List
	total   int64
}

func New(dir string, maxBytes int64, store blob.Store) (*Cache, error) {
	c := &Cache{dir: dir, maxBytes: maxBytes, store: store, entries: map[blob.Key]*entry{}, lru: list.New()}
	_ = os.RemoveAll(filepath.Join(dir, "tmp"))
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
		return nil, err
	}
	type found struct {
		key  blob.Key
		size int64
		mod  int64
	}
	var files []found
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		key := blob.Key(d.Name())
		if !key.Valid() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, found{key, info.Size(), info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b found) int { return cmp.Compare(b.mod, a.mod) })
	for _, f := range files {
		e := &entry{key: f.key, size: f.size}
		e.elem = c.lru.PushBack(f.key)
		c.entries[f.key] = e
		c.total += f.size
	}
	c.mu.Lock()
	c.evictLocked()
	c.mu.Unlock()
	return c, nil
}

func (c *Cache) Acquire(ctx context.Context, key blob.Key) (string, func(), error) {
	if !key.Valid() {
		return "", nil, fmt.Errorf("testcache: invalid key %q", key)
	}
	if path, release, ok := c.pin(key); ok {
		return path, release, nil
	}
	_, err, _ := c.group.Do(string(key), func() (any, error) {
		c.mu.Lock()
		_, ok := c.entries[key]
		c.mu.Unlock()
		if ok {
			return nil, nil
		}
		size, err := c.fetch(ctx, key)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		e := &entry{key: key, size: size}
		e.elem = c.lru.PushFront(key)
		c.entries[key] = e
		c.total += size
		c.mu.Unlock()
		return nil, nil
	})
	if err != nil {
		return "", nil, err
	}
	if path, release, ok := c.pin(key); ok {
		c.mu.Lock()
		c.evictLocked()
		c.mu.Unlock()
		return path, release, nil
	}
	return "", nil, fmt.Errorf("testcache: %s vanished after fetch", key)
}

func (c *Cache) pin(key blob.Key) (string, func(), bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", nil, false
	}
	e.pins++
	c.lru.MoveToFront(e.elem)
	var once sync.Once
	return c.path(key), func() {
		once.Do(func() {
			c.mu.Lock()
			e.pins--
			c.evictLocked()
			c.mu.Unlock()
		})
	}, true
}

func (c *Cache) fetch(ctx context.Context, key blob.Key) (int64, error) {
	rc, err := c.store.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("fetch test data %s: %w", key, err)
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Join(c.dir, "tmp"), "fetch-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), rc)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("fetch test data %s: %w", key, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != string(key) {
		return 0, fmt.Errorf("fetch test data %s: content hash is %s", key, got)
	}
	dst := c.path(key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return 0, err
	}
	return size, os.Rename(tmp.Name(), dst)
}

func (c *Cache) evictLocked() {
	for el := c.lru.Back(); el != nil && c.total > c.maxBytes; {
		prev := el.Prev()
		key := el.Value.(blob.Key)
		e := c.entries[key]
		if e.pins == 0 {
			if err := os.Remove(c.path(key)); err == nil || os.IsNotExist(err) {
				c.lru.Remove(el)
				delete(c.entries, key)
				c.total -= e.size
			}
		}
		el = prev
	}
}

func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func (c *Cache) path(key blob.Key) string {
	k := string(key)
	return filepath.Join(c.dir, k[:2], k)
}
