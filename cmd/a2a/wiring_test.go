package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// buildDeps threads migrationsDatabaseURL into the migration step and
// databaseURL into the pool, never the two conflated. DATABASE_URL points at
// an address nothing listens on and MIGRATIONS_DATABASE_URL at a schemeless
// string the migrator rejects at once with a distinctive parse error. The
// context times out after the first attempt, so the LOGGED retry line carries
// the real cause (a cancelled context would leave only ctx.Err() and make
// this test pass whatever URL the migration used). If buildDeps migrated
// through databaseURL, the cause would be a dial error instead.
func TestBuildDeps_MigratesThroughMigrationsDatabaseURLNotDatabaseURL(t *testing.T) {
	const (
		bogusMigrationsURL = "not-a-valid-connection-string"
		unreachableAppURL  = "postgres://u@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"
	)
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _, err := buildDeps(ctx, slog.New(slog.NewTextHandler(&logs, nil)), unreachableAppURL, bogusMigrationsURL)
	if err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	out := logs.String()
	if !strings.Contains(out, "retrying") || !strings.Contains(out, "run migrations") || !strings.Contains(out, "parse scheme") {
		t.Fatalf("log = %q, want the migration step to fail on the bogus MIGRATIONS_DATABASE_URL (parse scheme)", out)
	}
	if strings.Contains(out, "connection refused") || strings.Contains(out, "127.0.0.1") {
		t.Fatalf("log = %q: the migration step dialled DATABASE_URL", out)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("NP_A2A_TEST_SET", "v")
	t.Setenv("NP_A2A_TEST_EMPTY", "")
	if got := getenv("NP_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("NP_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
