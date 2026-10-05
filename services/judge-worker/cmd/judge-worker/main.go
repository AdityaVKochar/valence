package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AdityaVKochar/valence/pkg/server"
	"github.com/AdityaVKochar/valence/pkg/version"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Judge Worker exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "Print the version and exit")
	addr := flag.String("addr", envOr("WORKER_ADDR", ":8081"), "HTTP listen address for health and metrics")
	flag.Parse()
	if *showVersion {
		fmt.Println("judge-worker", version.String())
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	server.Health(mux, nil)

	slog.Info("Judge Worker starting", "version", version.String())
	return server.Run(ctx, *addr, mux, 30*time.Second)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
