package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/AdityaVKochar/valence/pkg/pg"
)

const EnvURL = "VALENCE_TEST_DATABASE_URL"

var (
	adminOnce sync.Once
	adminURL  string
	adminErr  error
)

func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := NewURL(t)
	ctx := context.Background()
	pool, err := pg.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pg.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func NewURL(t testing.TB) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres test in -short mode")
	}
	adminOnce.Do(func() { adminURL, adminErr = admin() })
	if adminErr != nil {
		t.Skipf("no Postgres for tests (set %s or start Docker): %v", EnvURL, adminErr)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "valence_test_" + hex.EncodeToString(b[:])
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			return
		}
		defer conn.Close(ctx)
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	u, _ := url.Parse(adminURL)
	u.Path = "/" + name
	return u.String()
}

func admin() (string, error) {
	if u := os.Getenv(EnvURL); u != "" {
		return u, nil
	}
	if os.Getenv("DOCKER_HOST") == "" {
		if _, err := os.Stat("/var/run/docker.sock"); err != nil {
			return "", fmt.Errorf("%s is unset and Docker is not running", EnvURL)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("valence"),
		postgres.WithUsername("valence"),
		postgres.WithPassword("valence"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(time.Minute)),
	)
	if err != nil {
		return "", err
	}
	return c.ConnectionString(ctx, "sslmode=disable")
}
