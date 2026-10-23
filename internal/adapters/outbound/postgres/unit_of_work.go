package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/node-performance/internal/application/ports"
)

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx that the
// repositories read with, so the same SQL runs against either.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// UnitOfWork is the pgx-backed ports.UnitOfWork: Do begins one transaction,
// carries it in the ctx handed to fn (see package pgtx), commits on a nil
// return and rolls back otherwise (an error or a panic). If ctx already
// carries a transaction Do joins it instead of beginning a nested one.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork constructs a UnitOfWork over pool.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork { return &UnitOfWork{pool: pool} }

// Do implements ports.UnitOfWork.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := pgtx.From(ctx); ok {
		return fn(ctx)
	}
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	// Rollback after a successful Commit is a harmless no-op (pgx.ErrTxClosed);
	// it also covers a panic in fn. The context survives the cancellation of
	// ctx so a cancelled request still rolls back.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(pgtx.With(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}

// queryFor returns the transaction carried by ctx, or pool when there is none
// (a repository used outside a UnitOfWork reads straight from the pool).
func queryFor(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := pgtx.From(ctx); ok {
		return tx
	}
	return pool
}

// atomically runs fn as ONE atomic write. Inside a unit of work it runs in a
// SAVEPOINT of the unit's transaction: when fn fails, its partial writes are
// rolled back to the savepoint and the unit's transaction stays usable (a
// failed statement would otherwise poison it with SQLSTATE 25P02), while the
// outermost Do still owns the commit. Outside a unit of work it begins,
// commits and rolls back its own transaction.
func atomically(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	var (
		tx  pgx.Tx
		err error
	)
	if outer, ok := pgtx.From(ctx); ok {
		tx, err = outer.Begin(ctx)
	} else {
		tx, err = pool.Begin(ctx)
	}
	if err != nil {
		return fmt.Errorf("begin write: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
