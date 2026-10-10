package app

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/cors"

	"github.com/AdityaVKochar/valence/gen/go/proto/valence/judge/v1/judgev1connect"
	"github.com/AdityaVKochar/valence/gen/go/proto/valence/v1/valencev1connect"
	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/config"
	"github.com/AdityaVKochar/valence/pkg/lang"
	"github.com/AdityaVKochar/valence/pkg/metrics"
	"github.com/AdityaVKochar/valence/pkg/obs"
	"github.com/AdityaVKochar/valence/pkg/queue"
	"github.com/AdityaVKochar/valence/pkg/queue/pgqueue"
	"github.com/AdityaVKochar/valence/pkg/server"
	"github.com/AdityaVKochar/valence/pkg/session/pgsession"
	"github.com/AdityaVKochar/valence/services/api/internal/auth"
	"github.com/AdityaVKochar/valence/services/api/internal/health"
	"github.com/AdityaVKochar/valence/services/api/internal/judgeapi"
	"github.com/AdityaVKochar/valence/services/api/internal/problems"
	"github.com/AdityaVKochar/valence/services/api/internal/submissions"
	"github.com/AdityaVKochar/valence/services/api/internal/users"
)

const maxRequestBytes = 1 << 20

type App struct {
	Public   http.Handler
	Internal http.Handler
	Auth     *auth.Service
	sessions *pgsession.Store
	log      *slog.Logger
}

type Options struct {
	Config    config.API
	Pool      *pgxpool.Pool
	Blobs     blob.Store
	Logger    *slog.Logger
	Registry  *prometheus.Registry
	Providers []auth.Provider
	Enqueue   submissions.EnqueueFunc
}

func New(opts Options) *App {
	cfg := opts.Config
	log := opts.Logger
	reg := opts.Registry
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	providers := opts.Providers
	if providers == nil {
		providers = DefaultProviders(cfg)
	}
	enqueue := opts.Enqueue
	if enqueue == nil {
		enqueue = func(ctx context.Context, tx pgx.Tx, job queue.Job) (string, error) {
			return pgqueue.EnqueueTx(ctx, tx, job)
		}
	}
	langs := lang.Default()
	sessions := pgsession.New(opts.Pool)
	authSvc := auth.New(auth.Options{
		Pool:      opts.Pool,
		Sessions:  sessions,
		TTL:       cfg.SessionTTL,
		WebOrigin: strings.TrimRight(cfg.WebOrigin, "/"),
		Secure:    strings.HasPrefix(cfg.PublicURL, "https://"),
		Dev:       cfg.Env == config.Dev,
		Providers: providers,
		Logger:    log,
	})
	rpc := metrics.NewRPC(reg)
	queueDepth(reg, pgqueue.New(opts.Pool, pgqueue.Options{Logger: log}))

	handlerOpts := connect.WithHandlerOptions(
		connect.WithInterceptors(rpc.Interceptor(), authSvc.Interceptor()),
		connect.WithReadMaxBytes(maxRequestBytes),
	)
	mux := http.NewServeMux()
	server.Health(mux, func(ctx context.Context) error { return opts.Pool.Ping(ctx) })
	userSvc := users.New(authSvc)
	mux.Handle(valencev1connect.NewHealthServiceHandler(health.New(opts.Pool), handlerOpts))
	mux.Handle(valencev1connect.NewAuthServiceHandler(userSvc, handlerOpts))
	mux.Handle(valencev1connect.NewUserServiceHandler(userSvc, handlerOpts))
	mux.Handle(valencev1connect.NewProblemServiceHandler(problems.New(opts.Pool, opts.Blobs), handlerOpts))
	mux.Handle(valencev1connect.NewSubmissionServiceHandler(submissions.New(submissions.Options{
		Pool:       opts.Pool,
		Languages:  langs,
		MaxCodeKiB: cfg.MaxCodeKiB,
		MaxActive:  cfg.MaxActiveSubmissions,
		Limiter:    submissions.NewRateLimiter(cfg.SubmitCooldown, 1),
		Enqueue:    enqueue,
	}), handlerOpts))
	authSvc.Routes(mux)

	corsMW := cors.New(cors.Options{
		AllowedOrigins:   []string{strings.TrimRight(cfg.WebOrigin, "/")},
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   connectcors.AllowedHeaders(),
		ExposedHeaders:   append(connectcors.ExposedHeaders(), "Retry-After", obs.RequestIDHeader),
		AllowCredentials: true,
		MaxAge:           7200,
	})

	internal := http.NewServeMux()
	server.Health(internal, func(ctx context.Context) error { return opts.Pool.Ping(ctx) })
	internal.Handle(judgev1connect.NewJudgeServiceHandler(judgeapi.New(opts.Pool, log),
		connect.WithInterceptors(rpc.Interceptor()),
		connect.WithReadMaxBytes(16<<20),
	))
	internal.Handle("GET /internal/blobs/{key}", judgeapi.BlobHandler(opts.Blobs))
	internal.Handle("HEAD /internal/blobs/{key}", judgeapi.BlobHandler(opts.Blobs))
	internal.Handle("GET /metrics", metrics.Handler(reg))

	return &App{
		Public:   obs.Chain(log, corsMW.Handler(mux)),
		Internal: obs.Chain(log, judgeapi.RequireToken(cfg.InternalToken, internal, "/healthz", "/readyz", "/metrics")),
		Auth:     authSvc,
		sessions: sessions,
		log:      log,
	}
}

func DefaultProviders(cfg config.API) []auth.Provider {
	var out []auth.Provider
	base := strings.TrimRight(cfg.PublicURL, "/")
	if cfg.GitHubClientID != "" {
		out = append(out, auth.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret, base+"/auth/github/callback"))
	}
	if cfg.GoogleClientID != "" {
		out = append(out, auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, base+"/auth/google/callback", cfg.GoogleAllowedDomains))
	}
	return out
}

func (a *App) CleanSessions(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := a.sessions.DeleteExpired(ctx); err != nil && ctx.Err() == nil {
			a.log.Warn("Delete expired sessions", "err", err)
		} else if n > 0 {
			a.log.Info("Deleted expired sessions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func queueDepth(reg prometheus.Registerer, q *pgqueue.Queue) {
	desc := prometheus.NewDesc("valence_queue_ready_jobs", "Jobs waiting to be leased, by kind.", []string{"kind"}, nil)
	reg.MustRegister(collector{desc: desc, collect: func(ch chan<- prometheus.Metric) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		depth, err := q.Depth(ctx)
		if err != nil {
			return
		}
		if _, ok := depth[submissions.JobKind]; !ok {
			depth[submissions.JobKind] = 0
		}
		for kind, n := range depth {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(n), kind)
		}
	}})
}

type collector struct {
	desc    *prometheus.Desc
	collect func(chan<- prometheus.Metric)
}

func (c collector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }
func (c collector) Collect(ch chan<- prometheus.Metric) { c.collect(ch) }
