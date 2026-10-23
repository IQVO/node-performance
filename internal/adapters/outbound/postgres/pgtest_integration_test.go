//go:build integration

package postgres_test

// This file boots ONE real Postgres through testcontainers per test binary and
// hands every test its own database, cloned from a template that already has
// the embedded migrations applied. It never reads DATABASE_URL and never skips:
// a missing Docker is a test failure (fleet rule,
// TestPostgresIntegrationTestsUseTestcontainers, which asks the package to
// import the testcontainers postgres module itself).

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
)

const (
	user         = "node_performance_test"
	password     = "node_performance_test" //nolint:gosec // throwaway credential of a local test container
	adminDB      = "postgres"
	templateName = "node_performance_template"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	startErr  error
	seq       atomic.Int64
)

func start() {
	ctx := context.Background()
	container, startErr = tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase(adminDB),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if startErr != nil {
		return
	}
	startErr = buildTemplate(ctx)
}

// buildTemplate creates the template database and applies the migrations to it
// twice: boot runs them on every start, so the second run must be a no-op.
func buildTemplate(ctx context.Context) error {
	if err := adminExec(ctx, `CREATE DATABASE `+templateName); err != nil {
		return err
	}
	url, err := databaseURL(ctx, templateName)
	if err != nil {
		return err
	}
	for range 2 {
		if err := postgres.RunMigrations(url); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
	}
	return nil
}

func databaseURL(ctx context.Context, db string) (string, error) {
	base, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", err
	}
	cfg, err := pgx.ParseConfig(base)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, cfg.Host, cfg.Port, db), nil
}

func adminExec(ctx context.Context, sql string) error {
	url, err := databaseURL(ctx, adminDB)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func boot(t *testing.T) {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatalf("start postgres container: %v", startErr)
	}
}

// freshURL returns the DSN of a FRESH database with the migrations applied, on the
// shared container.
func freshURL(t *testing.T) string {
	t.Helper()
	boot(t)
	ctx := context.Background()
	name := fmt.Sprintf("t_%d", seq.Add(1))
	if err := adminExec(ctx, `CREATE DATABASE `+name+` TEMPLATE `+templateName); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	url, err := databaseURL(ctx, name)
	if err != nil {
		t.Fatalf("database url: %v", err)
	}
	return url
}

// emptyURL returns the DSN of a FRESH database with NO migration applied, for
// the tests of the migrations themselves.
func emptyURL(t *testing.T) string {
	t.Helper()
	boot(t)
	ctx := context.Background()
	name := fmt.Sprintf("e_%d", seq.Add(1))
	if err := adminExec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	url, err := databaseURL(ctx, name)
	if err != nil {
		t.Fatalf("database url: %v", err)
	}
	return url
}

// newPool returns a pool on a fresh migrated database, closed when the test
// ends.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openPool(t, freshURL(t))
}

// openPool returns a pool on url, closed when the test ends.
func openPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// shutdownContainer terminates the shared container; TestMain calls it.
func shutdownContainer() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}
