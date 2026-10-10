package memqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/AdityaVKochar/valence/pkg/queue"
)

type entry struct {
	job         queue.Job
	seq         int64
	availableAt time.Time
	token       string
	leasedUntil time.Time
	attempts    int
}

type Queue struct {
	mu      sync.Mutex
	nextID  int64
	entries []*entry
	wake    chan struct{}
}

var _ queue.Queue = (*Queue)(nil)

func New() *Queue {
	return &Queue{wake: make(chan struct{})}
}

func (q *Queue) Enqueue(_ context.Context, job queue.Job) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job.DedupeKey != "" {
		for _, e := range q.entries {
			if e.job.DedupeKey == job.DedupeKey {
				return e.job.ID, nil
			}
		}
	}
	q.nextID++
	job.ID = strconv.FormatInt(q.nextID, 10)
	job.Payload = slices.Clone(job.Payload)
	q.entries = append(q.entries, &entry{job: job, seq: q.nextID, availableAt: time.Now()})
	q.broadcast()
	return job.ID, nil
}

func (q *Queue) Lease(ctx context.Context, kinds []string, ttl time.Duration) (queue.Lease, error) {
	for {
		q.mu.Lock()
		l, ok := q.tryLease(kinds, ttl)
		wake := q.wake
		q.mu.Unlock()
		if ok {
			return l, nil
		}
		select {
		case <-ctx.Done():
			return queue.Lease{}, ctx.Err()
		case <-wake:
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (q *Queue) tryLease(kinds []string, ttl time.Duration) (queue.Lease, bool) {
	now := time.Now()
	var best *entry
	for _, e := range q.entries {
		if !slices.Contains(kinds, e.job.Kind) || e.availableAt.After(now) || (e.token != "" && e.leasedUntil.After(now)) {
			continue
		}
		if best == nil || e.job.Priority > best.job.Priority || (e.job.Priority == best.job.Priority && e.seq < best.seq) {
			best = e
		}
	}
	if best == nil {
		return queue.Lease{}, false
	}
	best.attempts++
	best.token = newToken()
	best.leasedUntil = now.Add(ttl)
	return q.lease(best), true
}

func (q *Queue) Heartbeat(_ context.Context, l queue.Lease, ttl time.Duration) (queue.Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.find(l)
	if e == nil {
		return queue.Lease{}, queue.ErrLeaseLost
	}
	e.leasedUntil = time.Now().Add(ttl)
	return q.lease(e), nil
}

func (q *Queue) Ack(_ context.Context, l queue.Lease) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.find(l)
	if e == nil {
		return queue.ErrLeaseLost
	}
	q.entries = slices.DeleteFunc(q.entries, func(x *entry) bool { return x == e })
	return nil
}

func (q *Queue) Nack(_ context.Context, l queue.Lease, delay time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.find(l)
	if e == nil {
		return queue.ErrLeaseLost
	}
	e.token = ""
	e.availableAt = time.Now().Add(delay)
	q.broadcast()
	return nil
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

func (q *Queue) find(l queue.Lease) *entry {
	for _, e := range q.entries {
		if e.job.ID == l.Job.ID && e.token != "" && e.token == l.Token {
			return e
		}
	}
	return nil
}

func (q *Queue) lease(e *entry) queue.Lease {
	job := e.job
	job.Payload = slices.Clone(job.Payload)
	return queue.Lease{Job: job, Attempt: e.attempts, Token: e.token, Deadline: e.leasedUntil}
}

func (q *Queue) broadcast() {
	close(q.wake)
	q.wake = make(chan struct{})
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
