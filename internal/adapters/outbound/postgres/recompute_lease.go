package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// leaseKey is the advisory-lock key of the recompute scheduler: a stable
// string hashed inside Postgres (hashtextextended, the fleet idiom), so every
// replica computes the same 64-bit key without coordinating.
const leaseKey = "node-performance:recompute-scheduler"

// unlockTimeout bounds the best-effort unlock of the release function: the
// round is over, so nothing may block shutdown on it.
const unlockTimeout = 5 * time.Second

// RecomputeLease is the exclusive lease of the recompute scheduler: a
// session-level Postgres advisory try-lock, so of the replicas running the
// scheduler at most one recomputes at a time. The lock is held on a dedicated
// connection for the round's duration and freed by the release function; a
// connection that dies releases it when its session ends.
type RecomputeLease struct{ pool *pgxpool.Pool }

// NewRecomputeLease constructs the lease over pool.
func NewRecomputeLease(pool *pgxpool.Pool) *RecomputeLease { return &RecomputeLease{pool: pool} }

// Acquire tries to take the lease. ok=false (nil error) means another replica
// holds it and the caller must skip the round. On success the returned release
// function frees the lease and returns the connection; it is idempotent-safe
// to call once, never panics and never blocks longer than unlockTimeout.
func (l *RecomputeLease) Acquire(ctx context.Context) (release func(), ok bool, err error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire lease connection: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, leaseKey).Scan(&got); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("take recompute lease: %w", err)
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		// Best effort: a failed unlock (the connection died) still frees the
		// lock, because releasing the connection ends the session that held it.
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, leaseKey)
		conn.Release()
	}, true, nil
}
