package memcache

import (
	"testing"
	"time"
)

func TestCache(t *testing.T) {
	c := New()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	ctx := t.Context()
	_ = c.Set(ctx, "a", []byte("1"), time.Second)
	_ = c.Set(ctx, "b", []byte("2"), 0)
	if v, ok, _ := c.Get(ctx, "a"); !ok || string(v) != "1" {
		t.Fatalf("Get a = %q, %v", v, ok)
	}
	now = now.Add(time.Second)
	if _, ok, _ := c.Get(ctx, "a"); ok {
		t.Fatal("a should have expired")
	}
	if _, ok, _ := c.Get(ctx, "b"); !ok {
		t.Fatal("b has no TTL and should still be there")
	}
	_ = c.Delete(ctx, "b", "missing")
	if _, ok, _ := c.Get(ctx, "b"); ok {
		t.Fatal("b should be deleted")
	}
}
