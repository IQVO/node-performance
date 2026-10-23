package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/node-performance/internal/application/ports"
)

func TestTranslateCreate(t *testing.T) {
	pk := &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: pkScorecards}
	if err := translateCreate(fmt.Errorf("wrapped: %w", pk), "scorecard x"); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("a primary-key violation is a concurrent update: %v", err)
	}
	other := &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "something_else"}
	if err := translateCreate(other, "scorecard x"); errors.Is(err, ports.ErrConcurrentUpdate) || !errors.Is(err, other) {
		t.Fatalf("another unique violation is wrapped as is: %v", err)
	}
	plain := errors.New("boom")
	if err := translateCreate(plain, "scorecard x"); errors.Is(err, ports.ErrConcurrentUpdate) || !errors.Is(err, plain) {
		t.Fatalf("a plain error is wrapped as is: %v", err)
	}
	if _, ok := uniqueViolation(plain); ok {
		t.Fatal("a plain error is no unique violation")
	}
	if err := errStaleVersion("scorecard x", 3); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("errStaleVersion = %v", err)
	}
}
