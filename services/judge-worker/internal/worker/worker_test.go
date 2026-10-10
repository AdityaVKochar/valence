package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	judgev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1/judgev1connect"
	valencev1 "github.com/AdityaVKochar/valence/gen/go/proto/valence/v1"
	"github.com/AdityaVKochar/valence/pkg/judge"
	"github.com/AdityaVKochar/valence/pkg/queue"
	"github.com/AdityaVKochar/valence/pkg/queue/memqueue"
)

type fakeAPI struct {
	mu       sync.Mutex
	results  []*judgev1.ReportResultRequest
	stages   []judgev1.Stage
	final    bool
	finalCh  chan struct{}
	notFound bool
}

func newFakeAPI() *fakeAPI { return &fakeAPI{finalCh: make(chan struct{})} }

func (f *fakeAPI) GetJob(_ context.Context, req *connect.Request[judgev1.GetJobRequest]) (*connect.Response[judgev1.GetJobResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notFound {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("gone"))
	}
	if f.final {
		return connect.NewResponse(&judgev1.GetJobResponse{AlreadyFinalized: true}), nil
	}
	return connect.NewResponse(&judgev1.GetJobResponse{Job: &judgev1.JudgeJob{
		SubmissionId: req.Msg.SubmissionId, Attempt: req.Msg.Attempt, Language: "cpp17", Source: []byte("int main(){}"),
		TimeLimitMs: 1000, MemoryLimitKib: 65536, Checker: "exact",
		Tests: []*judgev1.JudgeTest{{Ordinal: 1, InputKey: "in", OutputKey: "out"}},
	}}), nil
}

func (f *fakeAPI) ReportProgress(_ context.Context, req *connect.Request[judgev1.ReportProgressRequest]) (*connect.Response[judgev1.ReportProgressResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stages = append(f.stages, req.Msg.Stage)
	return connect.NewResponse(&judgev1.ReportProgressResponse{Accepted: !f.final}), nil
}

func (f *fakeAPI) ReportResult(_ context.Context, req *connect.Request[judgev1.ReportResultRequest]) (*connect.Response[judgev1.ReportResultResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.final {
		return connect.NewResponse(&judgev1.ReportResultResponse{}), nil
	}
	f.final = true
	f.results = append(f.results, req.Msg)
	close(f.finalCh)
	return connect.NewResponse(&judgev1.ReportResultResponse{Applied: true}), nil
}

func (f *fakeAPI) result(t *testing.T) *judgev1.ReportResultRequest {
	t.Helper()
	select {
	case <-f.finalCh:
	case <-time.After(10 * time.Second):
		t.Fatal("no result was reported")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.results[0]
}

type fakeProvider struct {
	judge func(ctx context.Context, report func(judge.Progress)) (judge.Result, error)
	calls int
	mu    sync.Mutex
}

func (p *fakeProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeProvider) Name() string                 { return "fake" }
func (p *fakeProvider) Capabilities() judge.Caps     { return judge.Caps{} }
func (p *fakeProvider) Health(context.Context) error { return nil }
func (p *fakeProvider) Judge(ctx context.Context, _ judge.Job, report func(judge.Progress)) (judge.Result, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.judge(ctx, report)
}

func accepted(_ context.Context, report func(judge.Progress)) (judge.Result, error) {
	report(judge.Progress{Stage: judge.Compiling})
	report(judge.Progress{Stage: judge.Running, Test: 1})
	return judge.Result{Verdict: judge.Accepted, Provider: "fake", MaxTime: 12 * time.Millisecond, MaxMemoryKiB: 900,
		Tests: []judge.TestResult{{Ordinal: 1, Verdict: judge.Accepted, Time: 12 * time.Millisecond, MemoryKiB: 900}}}, nil
}

func setup(t *testing.T, api *fakeAPI) (judgev1connect.JudgeServiceClient, *memqueue.Queue) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(judgev1connect.NewJudgeServiceHandler(api))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	q := memqueue.New()
	payload, _ := json.Marshal(Payload{SubmissionID: 7, Attempt: 1})
	if _, err := q.Enqueue(context.Background(), queue.Job{Kind: JobKind, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	return judgev1connect.NewJudgeServiceClient(srv.Client(), srv.URL), q
}

func start(t *testing.T, opts Options) (stop func()) {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Backoff == nil {
		opts.Backoff = func(int) time.Duration { return 0 }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = New(opts).Run(ctx)
		close(done)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("worker did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func waitEmpty(t *testing.T, q *memqueue.Queue) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for q.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("queue still has %d jobs", q.Len())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestJudgesAndAcks(t *testing.T) {
	api := newFakeAPI()
	client, q := setup(t, api)
	start(t, Options{Queue: q, API: client, Provider: &fakeProvider{judge: accepted}, Slots: 2, Name: "w1"})

	r := api.result(t)
	if r.Verdict != valencev1.Verdict_VERDICT_ACCEPTED || r.Worker != "w1" || r.TimeMs != 12 || r.MemoryKib != 900 || len(r.Tests) != 1 || r.InfraRetries != 0 {
		t.Fatalf("result = %+v", r)
	}
	waitEmpty(t, q)
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.stages) < 3 || api.stages[0] != judgev1.Stage_STAGE_LEASED || api.stages[1] != judgev1.Stage_STAGE_COMPILING {
		t.Fatalf("stages = %v", api.stages)
	}
}

func TestInfrastructureErrorsEndInInternalError(t *testing.T) {
	api := newFakeAPI()
	client, q := setup(t, api)
	p := &fakeProvider{judge: func(context.Context, func(judge.Progress)) (judge.Result, error) {
		return judge.Result{}, errors.New("sandbox exploded")
	}}
	start(t, Options{Queue: q, API: client, Provider: p, Name: "w1", MaxDeliveries: 3})

	r := api.result(t)
	if r.Verdict != valencev1.Verdict_VERDICT_INTERNAL_ERROR || r.InfraRetries != 2 || r.Error != "sandbox exploded" {
		t.Fatalf("result = %+v", r)
	}
	if n := p.count(); n != 3 {
		t.Fatalf("provider ran %d times, want 3", n)
	}
	waitEmpty(t, q)
	api.mu.Lock()
	defer api.mu.Unlock()
	retryable := 0
	for _, s := range api.stages {
		if s == judgev1.Stage_STAGE_RETRYABLE_ERROR {
			retryable++
		}
	}
	if retryable != 2 {
		t.Fatalf("stages = %v, want two retryable errors", api.stages)
	}
}

func TestPermanentErrorIsNotRetried(t *testing.T) {
	api := newFakeAPI()
	client, q := setup(t, api)
	p := &fakeProvider{judge: func(context.Context, func(judge.Progress)) (judge.Result, error) {
		return judge.Result{}, judge.ErrPermanent
	}}
	start(t, Options{Queue: q, API: client, Provider: p, Name: "w1"})
	if r := api.result(t); r.Verdict != valencev1.Verdict_VERDICT_INTERNAL_ERROR || r.InfraRetries != 0 {
		t.Fatalf("result = %+v", r)
	}
	waitEmpty(t, q)
	if n := p.count(); n != 1 {
		t.Fatalf("provider ran %d times", n)
	}
}

func TestDropsJobsForDeletedSubmissions(t *testing.T) {
	api := newFakeAPI()
	api.notFound = true
	client, q := setup(t, api)
	p := &fakeProvider{judge: accepted}
	start(t, Options{Queue: q, API: client, Provider: p})
	waitEmpty(t, q)
	if p.count() != 0 {
		t.Fatal("judged a deleted submission")
	}
}

// deadQueue stands in for a worker that has lost its connection: heartbeats, acks and nacks never land.
type deadQueue struct{ queue.Queue }

func (deadQueue) Heartbeat(context.Context, queue.Lease, time.Duration) (queue.Lease, error) {
	return queue.Lease{}, errors.New("connection refused")
}
func (deadQueue) Ack(context.Context, queue.Lease) error { return errors.New("connection refused") }
func (deadQueue) Nack(context.Context, queue.Lease, time.Duration) error {
	return errors.New("connection refused")
}

func TestCrashedWorkersJobIsJudgedElsewhere(t *testing.T) {
	api := newFakeAPI()
	client, q := setup(t, api)
	hung := make(chan struct{})
	defer close(hung)
	stuck := &fakeProvider{judge: func(ctx context.Context, _ func(judge.Progress)) (judge.Result, error) {
		<-hung
		return judge.Result{}, ctx.Err()
	}}
	start(t, Options{Queue: deadQueue{q}, API: client, Provider: stuck, Name: "crashed", LeaseTTL: 200 * time.Millisecond})
	deadline := time.Now().Add(5 * time.Second)
	for stuck.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first worker never picked up the job")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start(t, Options{Queue: q, API: client, Provider: &fakeProvider{judge: accepted}, Name: "healthy", LeaseTTL: 200 * time.Millisecond})
	r := api.result(t)
	if r.Worker != "healthy" || r.Verdict != valencev1.Verdict_VERDICT_ACCEPTED || r.InfraRetries != 1 {
		t.Fatalf("result = %+v", r)
	}
}

func TestShutdownReturnsInFlightJobs(t *testing.T) {
	api := newFakeAPI()
	client, q := setup(t, api)
	running := make(chan struct{})
	p := &fakeProvider{judge: func(ctx context.Context, _ func(judge.Progress)) (judge.Result, error) {
		close(running)
		<-ctx.Done()
		return judge.Result{}, ctx.Err()
	}}
	stop := start(t, Options{Queue: q, API: client, Provider: p, Name: "w1", ShutdownGrace: 50 * time.Millisecond})
	<-running
	stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	l, err := q.Lease(ctx, []string{JobKind}, time.Minute)
	if err != nil {
		t.Fatalf("job was not returned to the queue: %v", err)
	}
	if l.Attempt != 2 {
		t.Fatalf("delivery = %d", l.Attempt)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.results) != 0 {
		t.Fatalf("shutdown reported a result: %+v", api.results)
	}
}

func TestShutdownLetsFastJobsFinish(t *testing.T) {
	api := newFakeAPI()
	client, q := setup(t, api)
	running := make(chan struct{})
	p := &fakeProvider{judge: func(ctx context.Context, report func(judge.Progress)) (judge.Result, error) {
		close(running)
		time.Sleep(100 * time.Millisecond)
		return accepted(ctx, report)
	}}
	stop := start(t, Options{Queue: q, API: client, Provider: p, Name: "w1", ShutdownGrace: 5 * time.Second})
	<-running
	stop()
	if r := api.result(t); r.Verdict != valencev1.Verdict_VERDICT_ACCEPTED {
		t.Fatalf("result = %+v", r)
	}
	waitEmpty(t, q)
}
