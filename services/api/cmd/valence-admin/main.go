package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/AdityaVKochar/valence/pkg/blob/localblob"
	"github.com/AdityaVKochar/valence/pkg/config"
	"github.com/AdityaVKochar/valence/pkg/pg"
	"github.com/AdityaVKochar/valence/pkg/problempkg"
	"github.com/AdityaVKochar/valence/services/api/internal/importer"
)

const usage = `Usage:
  valence-admin problem validate <dir>...   Check problem packages without touching the database
  valence-admin problem import <dir>...     Validate, store test files and upsert problems

import reads DATABASE_URL and BLOB_DIR like the API does.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) < 3 || args[0] != "problem" || (args[1] != "validate" && args[1] != "import") {
		return errors.New(usage)
	}
	var pkgs []*problempkg.Package
	failed := 0
	for _, dir := range args[2:] {
		p, err := problempkg.Load(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed++
			continue
		}
		pkgs = append(pkgs, p)
		if args[1] == "validate" {
			fmt.Printf("%s: ok (%d tests, %d solutions)\n", p.Slug, len(p.Tests), len(p.Solutions))
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d packages are invalid", failed, len(args)-2)
	}
	if args[1] == "validate" {
		return nil
	}

	cfg, err := config.LoadAPI()
	if err != nil {
		return err
	}
	pool, err := pg.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrateEnabled() {
		if err := pg.Migrate(ctx, pool); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	blobs, err := localblob.New(cfg.BlobDir)
	if err != nil {
		return err
	}
	im := importer.New(pool, blobs)
	for _, p := range pkgs {
		out, err := im.Import(ctx, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p.Slug, err)
		}
		fmt.Printf("%s: %s (revision %d, %d tests)\n", out.Slug, out.Result, out.Revision, out.Tests)
	}
	return nil
}
