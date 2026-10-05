package queue

import (
	"context"
	"errors"
	"time"
)

var ErrLeaseLost = errors.New("queue: lease lost")

type Job struct {
	ID        string
	Kind      string
	Priority  int
	Payload   []byte
	DedupeKey string
}

type Lease struct {
	Job      Job
	Attempt  int
	Token    string
	Deadline time.Time
}

type Queue interface {
	Enqueue(ctx context.Context, job Job) (string, error)
	Lease(ctx context.Context, kinds []string, ttl time.Duration) (Lease, error)
	Heartbeat(ctx context.Context, l Lease, ttl time.Duration) (Lease, error)
	Ack(ctx context.Context, l Lease) error
	Nack(ctx context.Context, l Lease, delay time.Duration) error
}
