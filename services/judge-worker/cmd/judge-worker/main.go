package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/sync/errgroup"

	"github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1/judgev1connect"
	"github.com/AdityaVKochar/valence/pkg/blob/httpblob"
	"github.com/AdityaVKochar/valence/pkg/config"
	"github.com/AdityaVKochar/valence/pkg/lang"
	"github.com/AdityaVKochar/valence/pkg/metrics"
	"github.com/AdityaVKochar/valence/pkg/obs"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/pkg/queue/pgqueue"
	"github.com/AdityaVKochar/valence/pkg/server"
	"github.com/AdityaVKochar/valence/pkg/version"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/engine"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/sandbox"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/testcache"
	"github.com/AdityaVKochar/valence/services/judge-worker/internal/worker"
)

func main() {
	sandbox.RunHelperIfRequested()
	if err := run(); err != nil {
		slog.Error("Judge Worker exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("judge-worker", version.String())
		return nil
	}

	cfg, err := config.LoadWorker()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := obs.NewLogger(os.Stderr, cfg.Env == config.Prod, cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sb, err := pickSandbox(ctx, cfg, log)
	if err != nil {
		return err
	}
	pool, err := pg.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	httpClient := &http.Client{Timeout: 2 * time.Minute}
	cache, err := testcache.New(cfg.CacheDir, cfg.CacheMaxMiB<<20, httpblob.New(cfg.APIInternalURL, cfg.InternalToken, httpClient))
	if err != nil {
		return err
	}
	var opts []engine.Option
	if sb.Name() == "local-unsafe" {
		opts = append(opts, engine.WithHostToolchains())
	}
	eng, err := engine.New(sb, lang.Default(), cache, cfg.WorkDir, cfg.Slots, opts...)
	if err != nil {
		return err
	}
	if len(eng.Capabilities().Languages) == 0 {
		return errors.New("no language toolchains are installed")
	}

	reg := metrics.NewRegistry()
	q := pgqueue.New(pool, pgqueue.Options{Logger: log})
	defer q.Close()
	host, _ := os.Hostname()
	w := worker.New(worker.Options{
		Queue: q,
		API: judgev1connect.NewJudgeServiceClient(httpClient, cfg.APIInternalURL,
			connect.WithInterceptors(bearer(cfg.InternalToken))),
		Provider: eng,
		Slots:    cfg.Slots,
		Name:     fmt.Sprintf("%s/%d", host, os.Getpid()),
		Logger:   log,
		Metrics:  worker.NewMetrics(reg),
	})

	mux := http.NewServeMux()
	server.Health(mux, func(ctx context.Context) error { return pool.Ping(ctx) })
	mux.Handle("GET /metrics", metrics.Handler(reg))

	log.Info("Judge Worker starting", "version", version.String(), "provider", sb.Name(), "slots", cfg.Slots,
		"languages", eng.Capabilities().Languages)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return w.Run(gctx) })
	g.Go(func() error { return server.Run(gctx, cfg.Addr, mux, 30*time.Second) })
	return g.Wait()
}

func pickSandbox(ctx context.Context, cfg config.Worker, log *slog.Logger) (sandbox.Sandbox, error) {
	isolate := func() (sandbox.Sandbox, error) {
		sb, err := sandbox.NewIsolate(cfg.IsolateBin, cfg.IsolateBoxBase, filepath.Join(cfg.WorkDir, "meta"))
		if err != nil {
			return nil, err
		}
		return sb, sb.Health(ctx)
	}
	unsafe := func() (sandbox.Sandbox, error) {
		if cfg.Env != config.Dev {
			return nil, errors.New("JUDGE_PROVIDER=local-unsafe runs submissions without a sandbox and only starts with VALENCE_ENV=dev")
		}
		log.Warn("Using local-unsafe: submissions run as your user with only rlimits. Never use this for untrusted code.")
		return sandbox.NewUnsafe(filepath.Join(cfg.WorkDir, "boxes"))
	}
	switch cfg.Provider {
	case "local-isolate":
		return isolate()
	case "local-unsafe":
		return unsafe()
	}
	sb, err := isolate()
	if err == nil {
		return sb, nil
	}
	log.Info("isolate is not usable; falling back to local-unsafe", "err", err)
	if cfg.Env != config.Dev {
		return nil, fmt.Errorf("JUDGE_PROVIDER=auto found no working isolate (%w) and local-unsafe is only allowed with VALENCE_ENV=dev", err)
	}
	return unsafe()
}

func bearer(token string) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, req)
		}
	})
}
