package memevents

import (
	"context"
	"slices"
	"sync"

	"github.com/AdityaVKochar/valence/pkg/events"
)

type sub struct {
	ctx context.Context
	fn  func(events.Event)
}

type Bus struct {
	mu   sync.RWMutex
	subs map[string][]*sub
}

var _ events.Bus = (*Bus)(nil)

func New() *Bus { return &Bus{subs: map[string][]*sub{}} }

func (b *Bus) Publish(_ context.Context, topic string, data []byte) error {
	b.mu.RLock()
	subs := slices.Clone(b.subs[topic])
	b.mu.RUnlock()
	for _, s := range subs {
		if s.ctx.Err() == nil {
			s.fn(events.Event{Topic: topic, Data: slices.Clone(data)})
		}
	}
	return nil
}

func (b *Bus) Subscribe(ctx context.Context, topic string, fn func(events.Event)) error {
	s := &sub{ctx: ctx, fn: fn}
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], s)
	b.mu.Unlock()
	context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.subs[topic] = slices.DeleteFunc(b.subs[topic], func(x *sub) bool { return x == s })
		b.mu.Unlock()
	})
	return nil
}
