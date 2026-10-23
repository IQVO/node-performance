package postgres

import (
	"errors"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRequireEnvNamesTheMissingVariable(t *testing.T) {
	if v, err := RequireEnv(env(map[string]string{"X": "1"}), "X"); err != nil || v != "1" {
		t.Fatalf("RequireEnv = %q, %v", v, err)
	}
	for _, m := range []map[string]string{{}, {"X": ""}} {
		_, err := RequireEnv(env(m), "X")
		if !errors.Is(err, ErrMissingEnv) || !strings.Contains(err.Error(), "X") {
			t.Fatalf("err = %v, want ErrMissingEnv naming X", err)
		}
	}
}

func TestDatabaseURLAndMigrationsURL(t *testing.T) {
	if _, err := DatabaseURL(env(nil)); !errors.Is(err, ErrMissingEnv) || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
	got, err := DatabaseURL(env(map[string]string{"DATABASE_URL": "postgres://a"}))
	if err != nil || got != "postgres://a" {
		t.Fatalf("DatabaseURL = %q, %v", got, err)
	}
	if got := MigrationsURL(env(nil), "postgres://a"); got != "postgres://a" {
		t.Fatalf("MigrationsURL falls back to DATABASE_URL: %q", got)
	}
	if got := MigrationsURL(env(map[string]string{"MIGRATIONS_DATABASE_URL": "postgres://direct"}), "postgres://a"); got != "postgres://direct" {
		t.Fatalf("MigrationsURL = %q", got)
	}
}
