package memevents

import (
	"context"
	"testing"

	"github.com/AdityaVKochar/valence/pkg/events"
)

func TestBus(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	var got []string
	_ = b.Subscribe(ctx, "verdict.1", func(e events.Event) { got = append(got, string(e.Data)) })
	_ = b.Publish(context.Background(), "verdict.1", []byte("a"))
	_ = b.Publish(context.Background(), "verdict.2", []byte("b"))
	cancel()
	_ = b.Publish(context.Background(), "verdict.1", []byte("c"))
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v, want [a]", got)
	}
}
