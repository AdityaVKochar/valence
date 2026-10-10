package pgqueue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AdityaVKochar/valence/pkg/pg/pgtest"
	"github.com/AdityaVKochar/valence/pkg/queue"
	"github.com/AdityaVKochar/valence/pkg/queue/queuetest"
)

func TestConformance(t *testing.T) {
	queuetest.Run(t, func(t *testing.T) queue.Queue {
		q := New(pgtest.New(t), Options{PollInterval: 100 * time.Millisecond})
		t.Cleanup(q.Close)
		return q
	})
}

func TestEnqueueTxRollsBack(t *testing.T) {
	pool := pgtest.New(t)
	q := New(pool, Options{PollInterval: 50 * time.Millisecond})
	t.Cleanup(q.Close)
	ctx := context.Background()
	boom := errors.New("boom")
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := EnqueueTx(ctx, tx, queue.Job{Kind: "judge", Payload: []byte("x")}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	lctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := q.Lease(lctx, []string{"judge"}, time.Minute); err == nil {
		t.Fatal("rolled-back job was leased")
	}
	depth, err := q.Depth(ctx)
	if err != nil || depth["judge"] != 0 {
		t.Fatalf("depth %v, %v", depth, err)
	}
}

func TestNotifyWakesIdleLease(t *testing.T) {
	pool := pgtest.New(t)
	q := New(pool, Options{PollInterval: time.Minute})
	t.Cleanup(q.Close)
	ctx := context.Background()
	got := make(chan error, 1)
	go func() {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err := q.Lease(lctx, []string{"judge"}, time.Minute)
		got <- err
	}()
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	if _, err := New(pool, Options{}).Enqueue(ctx, queue.Job{Kind: "judge", Payload: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("NOTIFY did not wake the lease; took %v", d)
	}
}
