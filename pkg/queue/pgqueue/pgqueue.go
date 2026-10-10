package pgqueue

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AdityaVKochar/valence/gen/go/db"
	"github.com/AdityaVKochar/valence/pkg/queue"
)

const channel = "jobs"

type Options struct {
	PollInterval time.Duration
	Logger       *slog.Logger
}

type Queue struct {
	pool *pgxpool.Pool
	q    *db.Queries
	opts Options

	listenOnce sync.Once
	mu         sync.Mutex
	wake       chan struct{}
	stop       context.CancelFunc
	done       chan struct{}
}

var _ queue.Queue = (*Queue)(nil)

func New(pool *pgxpool.Pool, opts Options) *Queue {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Queue{pool: pool, q: db.New(pool), opts: opts, wake: make(chan struct{})}
}

func (q *Queue) Enqueue(ctx context.Context, job queue.Job) (string, error) {
	return EnqueueTx(ctx, q.pool, job)
}

func EnqueueTx(ctx context.Context, tx db.DBTX, job queue.Job) (string, error) {
	var dedupe *string
	if job.DedupeKey != "" {
		dedupe = &job.DedupeKey
	}
	id, err := db.New(tx).EnqueueJob(ctx, db.EnqueueJobParams{
		Kind:      job.Kind,
		Priority:  int16(job.Priority),
		Payload:   job.Payload,
		DedupeKey: dedupe,
	})
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

func (q *Queue) Lease(ctx context.Context, kinds []string, ttl time.Duration) (queue.Lease, error) {
	q.listenOnce.Do(q.startListener)
	for {
		q.mu.Lock()
		wake := q.wake
		q.mu.Unlock()

		j, err := q.q.LeaseJob(ctx, db.LeaseJobParams{TtlSeconds: ttl.Seconds(), Kinds: kinds})
		if err == nil {
			return toLease(j), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			if ctx.Err() != nil {
				return queue.Lease{}, ctx.Err()
			}
			return queue.Lease{}, err
		}
		timer := time.NewTimer(q.opts.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return queue.Lease{}, ctx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (q *Queue) Heartbeat(ctx context.Context, l queue.Lease, ttl time.Duration) (queue.Lease, error) {
	id, token, err := parse(l)
	if err != nil {
		return queue.Lease{}, err
	}
	until, err := q.q.HeartbeatJob(ctx, db.HeartbeatJobParams{TtlSeconds: ttl.Seconds(), ID: id, LeaseToken: &token})
	if errors.Is(err, pgx.ErrNoRows) {
		return queue.Lease{}, queue.ErrLeaseLost
	}
	if err != nil {
		return queue.Lease{}, err
	}
	if until != nil {
		l.Deadline = *until
	}
	return l, nil
}

func (q *Queue) Ack(ctx context.Context, l queue.Lease) error {
	id, token, err := parse(l)
	if err != nil {
		return err
	}
	n, err := q.q.AckJob(ctx, db.AckJobParams{ID: id, LeaseToken: &token})
	if err != nil {
		return err
	}
	if n == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

func (q *Queue) Nack(ctx context.Context, l queue.Lease, delay time.Duration) error {
	return q.NackWithError(ctx, l, delay, "")
}

func (q *Queue) NackWithError(ctx context.Context, l queue.Lease, delay time.Duration, reason string) error {
	id, token, err := parse(l)
	if err != nil {
		return err
	}
	var lastErr *string
	if reason != "" {
		lastErr = &reason
	}
	n, err := q.q.NackJob(ctx, db.NackJobParams{DelaySeconds: delay.Seconds(), LastError: lastErr, ID: id, LeaseToken: &token})
	if err != nil {
		return err
	}
	if n == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

func (q *Queue) Depth(ctx context.Context) (map[string]int64, error) {
	rows, err := q.q.CountJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Kind] = r.Ready
	}
	return out, nil
}

func (q *Queue) Close() {
	q.mu.Lock()
	stop, done := q.stop, q.done
	q.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

func (q *Queue) startListener() {
	ctx, cancel := context.WithCancel(context.Background())
	q.mu.Lock()
	q.stop = cancel
	q.done = make(chan struct{})
	q.mu.Unlock()
	ready := make(chan struct{})
	go func() {
		defer close(q.done)
		var once sync.Once
		for ctx.Err() == nil {
			err := q.listen(ctx, func() { once.Do(func() { close(ready) }) })
			if ctx.Err() != nil {
				break
			}
			once.Do(func() { close(ready) })
			q.opts.Logger.Warn("Queue listener lost its connection; polling until it reconnects", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		once.Do(func() { close(ready) })
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	}
}

func (q *Queue) listen(ctx context.Context, ready func()) error {
	conn, err := q.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	defer func() {
		if ctx.Err() != nil {
			conn.Conn().Close(context.Background())
		} else {
			_, _ = conn.Exec(context.Background(), "UNLISTEN "+channel)
		}
	}()
	ready()
	q.broadcast()
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		q.broadcast()
	}
}

func (q *Queue) broadcast() {
	q.mu.Lock()
	close(q.wake)
	q.wake = make(chan struct{})
	q.mu.Unlock()
}

func toLease(j db.Job) queue.Lease {
	l := queue.Lease{
		Job: queue.Job{
			ID:       strconv.FormatInt(j.ID, 10),
			Kind:     j.Kind,
			Priority: int(j.Priority),
			Payload:  j.Payload,
		},
		Attempt: int(j.Attempts),
	}
	if j.DedupeKey != nil {
		l.Job.DedupeKey = *j.DedupeKey
	}
	if j.LeaseToken != nil {
		l.Token = j.LeaseToken.String()
	}
	if j.LeasedUntil != nil {
		l.Deadline = *j.LeasedUntil
	}
	return l
}

func parse(l queue.Lease) (int64, uuid.UUID, error) {
	id, err := strconv.ParseInt(l.Job.ID, 10, 64)
	if err != nil {
		return 0, uuid.UUID{}, queue.ErrLeaseLost
	}
	token, err := uuid.Parse(l.Token)
	if err != nil {
		return 0, uuid.UUID{}, queue.ErrLeaseLost
	}
	return id, token, nil
}
