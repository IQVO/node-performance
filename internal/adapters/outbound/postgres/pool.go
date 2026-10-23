// Package postgres provides the pgx-backed implementations of the outbound
// ports plus a golang-migrate runner over the SQL migrations EMBEDDED in this
// package (migrations/), so the binary needs no migrations directory on disk.
//
// Every query is parameterised; no SQL is built from values. Ids are ordered
// byte-wise (COLLATE "C") so a list reads in the same order as the in-memory
// doubles. Timestamps are timestamptz (microsecond precision); the domain
// truncates every instant it stores to the microsecond (shared.Instant) so a
// stored time reads back unchanged.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxConns is this adapter's per-process connection ceiling.
const MaxConns = 10

// StatementTimeout bounds how long a single query may hold a connection.
const StatementTimeout = "5s"

// NewPool opens a connection pool against databaseURL with MaxConns and
// StatementTimeout applied to every connection.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	config.MaxConns = MaxConns
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET statement_timeout = '"+StatementTimeout+"'")
		return err
	}
	return pgxpool.NewWithConfig(ctx, config)
}
