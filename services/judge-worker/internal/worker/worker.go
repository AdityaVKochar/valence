package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect"

	judgev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1/judgev1connect"
	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/judge"
	"github.com/AdityaVKochar/valence/pkg/queue"
)

const JobKind = "judge"

type Payload struct {
	SubmissionID int64 `json:"submission_id"`
	Attempt      int32 `json:"attempt"`
}

type Options struct {
	Queue    queue.Queue
	API      judgev1connect.JudgeServiceClient
	Provider judge.Provider
	Slots    int
	Name     string
	Logger   *slog.Logger
	Metrics  *Metrics

	// LeaseTTL is how long a lease lasts without a heartbeat. Heartbeats go out every third of it.
	LeaseTTL time.Duration
	// MaxDeliveries is how many times a job is tried before its submission is finalized as IE.
	MaxDeliveries int
	// ShutdownGrace is how long in-flight jobs may keep running after Run's context is cancelled.
	ShutdownGrace time.Duration
	// Backoff returns the nack delay after the given delivery failed.
	Backoff func(delivery int) time.Duration
}

type Worker struct {
	opts Options
	log  *slog.Logger
}

func New(opts Options) *Worker {
	if opts.Slots < 1 {
		opts.Slots = 1
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = time.Minute
	}
	if opts.MaxDeliveries <= 0 {
		opts.MaxDeliveries = 3
	}
	if opts.ShutdownGrace <= 0 {
		opts.ShutdownGrace = 25 * time.Second
	}
	if opts.Backoff == nil {
		opts.Backoff = func(n int) time.Duration { return time.Duration(1<<min(n, 6)) * time.Second }
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Metrics == nil {
		opts.Metrics = NewMetrics(nil)
	}
	return &Worker{opts: opts, log: opts.Logger}
}

// Run leases and judges jobs on every slot until ctx is cancelled, then waits for in-flight
// jobs for up to ShutdownGrace and nacks whatever is still running.
func (w *Worker) Run(ctx context.Context) error {
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()
	stop := context.AfterFunc(ctx, func() {
		t := time.NewTimer(w.opts.ShutdownGrace)
		defer t.Stop()
		select {
		case <-t.C:
			cancelJobs()
		case <-jobCtx.Done():
		}
	})
	defer stop()

	var wg sync.WaitGroup
	for slot := range w.opts.Slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx, jobCtx, slot)
		}()
	}
	wg.Wait()
	return nil
}

func (w *Worker) loop(ctx, jobCtx context.Context, slot int) {
	failures := 0
	for ctx.Err() == nil {
		l, err := w.opts.Queue.Lease(ctx, []string{JobKind}, w.opts.LeaseTTL)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			w.log.Error("Lease job", "slot", slot, "err", err)
			select {
			case <-time.After(min(time.Duration(failures)*time.Second, 10*time.Second)):
			case <-ctx.Done():
				return
			}
			continue
		}
		failures = 0
		w.opts.Metrics.busy.Inc()
		w.handle(jobCtx, l)
		w.opts.Metrics.busy.Dec()
	}
}

type outcome int

const (
	done outcome = iota
	retry
	leaseLost
	shutdown
)

func (w *Worker) handle(ctx context.Context, l queue.Lease) {
	var p Payload
	if err := json.Unmarshal(l.Job.Payload, &p); err != nil || p.SubmissionID <= 0 || p.Attempt <= 0 {
		w.log.Error("Dropping malformed judge job", "job_id", l.Job.ID, "payload", string(l.Job.Payload))
		w.ack(l)
		return
	}
	log := w.log.With("submission_id", p.SubmissionID, "attempt", p.Attempt, "delivery", l.Attempt)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var hb sync.WaitGroup
	hb.Add(1)
	go func() {
		defer hb.Done()
		w.heartbeat(ctx, l, cancel)
	}()
	defer hb.Wait()
	defer cancel(nil)

	start := time.Now()
	out, err := w.judge(ctx, log, l, p)
	if out == done {
		w.opts.Metrics.duration.Observe(time.Since(start).Seconds())
	}
	if errors.Is(context.Cause(ctx), queue.ErrLeaseLost) {
		out = leaseLost
	}
	switch out {
	case done:
		w.ack(l)
	case leaseLost:
		log.Warn("Lease lost; another worker owns this job now")
	case shutdown:
		log.Info("Returning job to the queue for shutdown")
		w.nack(l, 0, "worker shut down")
	case retry:
		w.opts.Metrics.infraErrors.Inc()
		delay := w.opts.Backoff(l.Attempt)
		log.Warn("Judging failed; will retry", "err", err, "retry_in", delay)
		w.report(context.WithoutCancel(ctx), p, judgev1.Stage_STAGE_RETRYABLE_ERROR, 0)
		w.nack(l, delay, err.Error())
	}
}

func (w *Worker) judge(ctx context.Context, log *slog.Logger, l queue.Lease, p Payload) (outcome, error) {
	fail := func(err error) (outcome, error) {
		if ctx.Err() != nil {
			if errors.Is(context.Cause(ctx), queue.ErrLeaseLost) {
				return leaseLost, err
			}
			return shutdown, err
		}
		if l.Attempt >= w.opts.MaxDeliveries {
			log.Error("Giving up on submission after repeated infrastructure errors", "err", err)
			return w.finalizeIE(ctx, p, l, err)
		}
		return retry, err
	}

	resp, err := w.opts.API.GetJob(ctx, connect.NewRequest(&judgev1.GetJobRequest{SubmissionId: p.SubmissionID, Attempt: p.Attempt}))
	if connect.CodeOf(err) == connect.CodeNotFound {
		log.Warn("Submission no longer exists; dropping its job")
		return done, nil
	}
	if err != nil {
		return fail(fmt.Errorf("get job: %w", err))
	}
	if resp.Msg.AlreadyFinalized {
		return done, nil
	}
	if l.Attempt > w.opts.MaxDeliveries {
		return w.finalizeIE(ctx, p, l, fmt.Errorf("job was delivered %d times without a result", l.Attempt-1))
	}
	if ok, err := w.progress(ctx, p, judgev1.Stage_STAGE_LEASED, 0); err != nil {
		return fail(fmt.Errorf("report leased: %w", err))
	} else if !ok {
		return done, nil
	}

	job := toJob(resp.Msg.Job)
	var last time.Time
	var lastStage judge.Stage
	res, err := w.opts.Provider.Judge(ctx, job, func(pr judge.Progress) {
		if pr.Stage == lastStage && time.Since(last) < 250*time.Millisecond {
			return
		}
		last, lastStage = time.Now(), pr.Stage
		w.report(ctx, p, stageToProto(pr.Stage), int32(pr.Test))
	})
	if errors.Is(err, judge.ErrPermanent) {
		log.Error("Submission cannot be judged", "err", err)
		return w.finalizeIE(ctx, p, l, err)
	}
	if err != nil {
		return fail(err)
	}
	req := &judgev1.ReportResultRequest{
		SubmissionId:  p.SubmissionID,
		Attempt:       p.Attempt,
		Verdict:       verdictToProto(res.Verdict),
		CompileOutput: res.CompileOutput,
		TimeMs:        int32(res.MaxTime.Milliseconds()),
		MemoryKib:     int32(res.MaxMemoryKiB),
		Provider:      res.Provider,
		Worker:        w.opts.Name,
		InfraRetries:  int32(l.Attempt - 1),
	}
	for _, t := range res.Tests {
		req.Tests = append(req.Tests, &judgev1.JudgeTestResult{
			Ordinal:   int32(t.Ordinal),
			Verdict:   verdictToProto(t.Verdict),
			TimeMs:    int32(t.Time.Milliseconds()),
			MemoryKib: int32(t.MemoryKiB),
		})
	}
	if _, err := w.opts.API.ReportResult(ctx, connect.NewRequest(req)); err != nil {
		return fail(fmt.Errorf("report result: %w", err))
	}
	w.opts.Metrics.verdicts.WithLabelValues(string(res.Verdict)).Inc()
	log.Info("Judged submission", "verdict", res.Verdict, "time_ms", req.TimeMs, "memory_kib", req.MemoryKib)
	return done, nil
}

func (w *Worker) finalizeIE(ctx context.Context, p Payload, l queue.Lease, cause error) (outcome, error) {
	_, err := w.opts.API.ReportResult(ctx, connect.NewRequest(&judgev1.ReportResultRequest{
		SubmissionId: p.SubmissionID,
		Attempt:      p.Attempt,
		Verdict:      valencev1.Verdict_VERDICT_INTERNAL_ERROR,
		Provider:     w.opts.Provider.Name(),
		Worker:       w.opts.Name,
		InfraRetries: int32(l.Attempt - 1),
		Error:        cause.Error(),
	}))
	if err != nil {
		return retry, fmt.Errorf("report internal error: %w", err)
	}
	w.opts.Metrics.verdicts.WithLabelValues(string(judge.InternalError)).Inc()
	return done, cause
}

func (w *Worker) heartbeat(ctx context.Context, l queue.Lease, cancel context.CancelCauseFunc) {
	t := time.NewTicker(w.opts.LeaseTTL / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next, err := w.opts.Queue.Heartbeat(ctx, l, w.opts.LeaseTTL)
		switch {
		case errors.Is(err, queue.ErrLeaseLost):
			cancel(queue.ErrLeaseLost)
			return
		case err != nil:
			if ctx.Err() == nil {
				w.log.Warn("Heartbeat failed", "job_id", l.Job.ID, "err", err)
			}
		default:
			l = next
		}
	}
}

func (w *Worker) progress(ctx context.Context, p Payload, stage judgev1.Stage, test int32) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := w.opts.API.ReportProgress(ctx, connect.NewRequest(&judgev1.ReportProgressRequest{
		SubmissionId: p.SubmissionID,
		Attempt:      p.Attempt,
		Stage:        stage,
		Test:         test,
		Provider:     w.opts.Provider.Name(),
		Worker:       w.opts.Name,
	}))
	if err != nil {
		return false, err
	}
	return resp.Msg.Accepted, nil
}

// report sends best-effort progress; judging carries on if it fails.
func (w *Worker) report(ctx context.Context, p Payload, stage judgev1.Stage, test int32) {
	if _, err := w.progress(ctx, p, stage, test); err != nil && ctx.Err() == nil {
		w.log.Debug("Report progress", "submission_id", p.SubmissionID, "stage", stage, "err", err)
	}
}

func (w *Worker) ack(l queue.Lease) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.opts.Queue.Ack(ctx, l); err != nil {
		w.log.Warn("Ack job", "job_id", l.Job.ID, "err", err)
	}
}

type nackWithError interface {
	NackWithError(ctx context.Context, l queue.Lease, delay time.Duration, reason string) error
}

func (w *Worker) nack(l queue.Lease, delay time.Duration, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	if q, ok := w.opts.Queue.(nackWithError); ok {
		err = q.NackWithError(ctx, l, delay, reason)
	} else {
		err = w.opts.Queue.Nack(ctx, l, delay)
	}
	if err != nil {
		w.log.Warn("Nack job", "job_id", l.Job.ID, "err", err)
	}
}

func toJob(j *judgev1.JudgeJob) judge.Job {
	out := judge.Job{
		SubmissionID:   j.SubmissionId,
		Attempt:        int(j.Attempt),
		Language:       j.Language,
		Source:         j.Source,
		TimeLimit:      time.Duration(j.TimeLimitMs) * time.Millisecond,
		MemoryLimitKiB: int64(j.MemoryLimitKib),
		Checker:        j.Checker,
	}
	for _, t := range j.Tests {
		out.Tests = append(out.Tests, judge.Test{Ordinal: int(t.Ordinal), Input: blob.Key(t.InputKey), Output: blob.Key(t.OutputKey)})
	}
	return out
}

func stageToProto(s judge.Stage) judgev1.Stage {
	switch s {
	case judge.Compiling:
		return judgev1.Stage_STAGE_COMPILING
	case judge.Running:
		return judgev1.Stage_STAGE_RUNNING
	case judge.Checking:
		return judgev1.Stage_STAGE_CHECKING
	}
	return judgev1.Stage_STAGE_UNSPECIFIED
}

var verdicts = map[judge.Verdict]valencev1.Verdict{
	judge.Accepted:            valencev1.Verdict_VERDICT_ACCEPTED,
	judge.WrongAnswer:         valencev1.Verdict_VERDICT_WRONG_ANSWER,
	judge.TimeLimitExceeded:   valencev1.Verdict_VERDICT_TIME_LIMIT_EXCEEDED,
	judge.MemoryLimitExceeded: valencev1.Verdict_VERDICT_MEMORY_LIMIT_EXCEEDED,
	judge.RuntimeError:        valencev1.Verdict_VERDICT_RUNTIME_ERROR,
	judge.OutputLimitExceeded: valencev1.Verdict_VERDICT_OUTPUT_LIMIT_EXCEEDED,
	judge.CompilationError:    valencev1.Verdict_VERDICT_COMPILATION_ERROR,
	judge.InternalError:       valencev1.Verdict_VERDICT_INTERNAL_ERROR,
}

func verdictToProto(v judge.Verdict) valencev1.Verdict {
	if p, ok := verdicts[v]; ok {
		return p
	}
	return valencev1.Verdict_VERDICT_INTERNAL_ERROR
}
