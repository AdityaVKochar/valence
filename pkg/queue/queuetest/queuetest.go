package queuetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AdityaVKochar/valence/pkg/queue"
)

func Run(t *testing.T, newQueue func(t *testing.T) queue.Queue) {
	t.Run("EnqueueLeaseAck", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		id, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte(`{"n":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		l := mustLease(t, q, []string{"judge"}, time.Minute)
		if l.Job.ID != id || string(l.Job.Payload) != `{"n":1}` || l.Job.Kind != "judge" || l.Attempt != 1 || l.Token == "" {
			t.Fatalf("unexpected lease %+v", l)
		}
		if time.Until(l.Deadline) < 30*time.Second {
			t.Fatalf("deadline %v too early", l.Deadline)
		}
		if err := q.Ack(ctx, l); err != nil {
			t.Fatal(err)
		}
		expectEmpty(t, q, []string{"judge"})
	})

	t.Run("DedupeKey", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		a, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("a"), DedupeKey: "judge:1:1"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("b"), DedupeKey: "judge:1:1"})
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("dedupe returned %s and %s", a, b)
		}
		l := mustLease(t, q, []string{"judge"}, time.Minute)
		if string(l.Job.Payload) != "a" {
			t.Fatalf("payload %q, want the first job's", l.Job.Payload)
		}
		expectEmpty(t, q, []string{"judge"})
	})

	t.Run("PriorityThenAge", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		for i, p := range []int{0, 5, 0, 5, 1} {
			if _, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Priority: p, Payload: fmt.Appendf(nil, "%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
		var got []string
		for range 5 {
			l := mustLease(t, q, []string{"judge"}, time.Minute)
			got = append(got, string(l.Job.Payload))
			if err := q.Ack(ctx, l); err != nil {
				t.Fatal(err)
			}
		}
		if fmt.Sprint(got) != "[1 3 4 0 2]" {
			t.Fatalf("lease order %v, want [1 3 4 0 2]", got)
		}
	})

	t.Run("Kinds", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, queue.Job{Kind: "other", Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		expectEmpty(t, q, []string{"judge"})
		l := mustLease(t, q, []string{"judge", "other"}, time.Minute)
		if l.Job.Kind != "other" {
			t.Fatalf("kind %q", l.Job.Kind)
		}
	})

	t.Run("NoDoubleDelivery", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		const jobs = 60
		for i := range jobs {
			if _, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: fmt.Appendf(nil, "%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
		var mu sync.Mutex
		seen := map[string]int{}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					lctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
					l, err := q.Lease(lctx, []string{"judge"}, time.Minute)
					cancel()
					if err != nil {
						return
					}
					mu.Lock()
					seen[string(l.Job.Payload)]++
					mu.Unlock()
					if err := q.Ack(ctx, l); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		wg.Wait()
		if len(seen) != jobs {
			t.Fatalf("delivered %d distinct jobs, want %d", len(seen), jobs)
		}
		for p, n := range seen {
			if n != 1 {
				t.Fatalf("job %s delivered %d times", p, n)
			}
		}
	})

	t.Run("ExpiredLeaseRedelivered", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		first := mustLease(t, q, []string{"judge"}, 300*time.Millisecond)
		second := mustLeaseWithin(t, q, []string{"judge"}, time.Minute, 10*time.Second)
		if second.Job.ID != first.Job.ID || second.Attempt != 2 || second.Token == first.Token {
			t.Fatalf("redelivery: first %+v second %+v", first, second)
		}
		if err := q.Ack(ctx, first); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("Ack with stale lease = %v, want ErrLeaseLost", err)
		}
		if _, err := q.Heartbeat(ctx, first, time.Minute); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("Heartbeat with stale lease = %v, want ErrLeaseLost", err)
		}
		if err := q.Nack(ctx, first, 0); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("Nack with stale lease = %v, want ErrLeaseLost", err)
		}
		if err := q.Ack(ctx, second); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("HeartbeatExtends", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		l := mustLease(t, q, []string{"judge"}, 400*time.Millisecond)
		l2, err := q.Heartbeat(ctx, l, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !l2.Deadline.After(l.Deadline) || l2.Token != l.Token {
			t.Fatalf("heartbeat did not extend: %+v -> %+v", l, l2)
		}
		time.Sleep(700 * time.Millisecond)
		expectEmpty(t, q, []string{"judge"})
		if err := q.Ack(ctx, l2); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("NackDelay", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		l := mustLease(t, q, []string{"judge"}, time.Minute)
		if err := q.Nack(ctx, l, 700*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		expectEmpty(t, q, []string{"judge"})
		l2 := mustLeaseWithin(t, q, []string{"judge"}, time.Minute, 10*time.Second)
		if l2.Attempt != 2 {
			t.Fatalf("attempt %d after nack, want 2", l2.Attempt)
		}
	})

	t.Run("LeaseWakesOnEnqueue", func(t *testing.T) {
		q := newQueue(t)
		ctx := context.Background()
		got := make(chan queue.Lease, 1)
		go func() {
			lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			l, err := q.Lease(lctx, []string{"judge"}, time.Minute)
			if err == nil {
				got <- l
			}
			close(got)
		}()
		time.Sleep(200 * time.Millisecond)
		start := time.Now()
		if _, err := q.Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		l, ok := <-got
		if !ok {
			t.Fatal("Lease returned without a job")
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("Lease woke after %v, want well under the polling interval", d)
		}
		_ = q.Ack(ctx, l)
	})

	t.Run("LeaseHonoursContext", func(t *testing.T) {
		q := newQueue(t)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := q.Lease(ctx, []string{"judge"}, time.Minute)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Lease on empty queue = %v, want DeadlineExceeded", err)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("Lease ignored the context deadline")
		}
	})
}

func mustLease(t *testing.T, q queue.Queue, kinds []string, ttl time.Duration) queue.Lease {
	t.Helper()
	return mustLeaseWithin(t, q, kinds, ttl, 2*time.Second)
}

func mustLeaseWithin(t *testing.T, q queue.Queue, kinds []string, ttl, wait time.Duration) queue.Lease {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	l, err := q.Lease(ctx, kinds, ttl)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	return l
}

func expectEmpty(t *testing.T, q queue.Queue, kinds []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if l, err := q.Lease(ctx, kinds, time.Minute); err == nil {
		t.Fatalf("expected no job, leased %+v", l)
	}
}
