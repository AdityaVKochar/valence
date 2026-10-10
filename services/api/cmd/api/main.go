package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/AdityaVKochar/valence/pkg/blob/localblob"
	"github.com/AdityaVKochar/valence/pkg/config"
	"github.com/AdityaVKochar/valence/pkg/obs"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/pkg/server"
	"github.com/AdityaVKochar/valence/pkg/version"
	"github.com/AdityaVKochar/valence/services/api/internal/app"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "Print the version and exit")
	addr := flag.String("addr", "", "HTTP listen address (overrides API_ADDR)")
	flag.Parse()
	if *showVersion {
		fmt.Println("API", version.String())
		return nil
	}

	cfg, err := config.LoadAPI()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	log := obs.NewLogger(os.Stderr, cfg.Env == config.Prod, cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pg.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrateEnabled() {
		if err := pg.Migrate(ctx, pool); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		log.Info("Database migrated")
	}
	blobs, err := localblob.New(cfg.BlobDir)
	if err != nil {
		return err
	}

	a := app.New(app.Options{Config: cfg, Pool: pool, Blobs: blobs, Logger: log})
	if cfg.Env == config.Dev {
		log.Warn("Development login is enabled at /auth/dev; never run VALENCE_ENV=dev in production")
	}
	log.Info("API starting", "version", version.String(), "env", cfg.Env, "auth_providers", len(a.Auth.Providers()))

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return server.Run(ctx, cfg.Addr, a.Public, 15*time.Second) })
	g.Go(func() error { return server.Run(ctx, cfg.InternalAddr, a.Internal, 15*time.Second) })
	g.Go(func() error { a.CleanSessions(ctx, time.Hour); return nil })
	return g.Wait()
}
