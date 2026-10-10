package memcache

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/AdityaVKochar/valence/pkg/cache"
)

type item struct {
	value   []byte
	expires time.Time
}

type Cache struct {
	mu    sync.Mutex
	items map[string]item
	now   func() time.Time
}

var _ cache.Cache = (*Cache)(nil)

func New() *Cache { return &Cache{items: map[string]item{}, now: time.Now} }

func (c *Cache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok {
		return nil, false, nil
	}
	if !it.expires.IsZero() && !c.now().Before(it.expires) {
		delete(c.items, key)
		return nil, false, nil
	}
	return slices.Clone(it.value), true, nil
}

func (c *Cache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	it := item{value: slices.Clone(value)}
	if ttl > 0 {
		it.expires = c.now().Add(ttl)
	}
	c.items[key] = it
	return nil
}

func (c *Cache) Delete(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		delete(c.items, k)
	}
	return nil
}
