package events

import "context"

type Event struct {
	Topic string
	Data  []byte
}

type Bus interface {
	Publish(ctx context.Context, topic string, data []byte) error
	Subscribe(ctx context.Context, topic string, fn func(Event)) error
}
