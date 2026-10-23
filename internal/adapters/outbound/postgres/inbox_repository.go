package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/application/ports"
)

// InboxRepo is the Postgres ports.ProcessedEvents.
type InboxRepo struct{ pool *pgxpool.Pool }

var _ ports.ProcessedEvents = (*InboxRepo)(nil)

// NewInboxRepo constructs an InboxRepo over pool.
func NewInboxRepo(pool *pgxpool.Pool) *InboxRepo { return &InboxRepo{pool: pool} }

// MarkProcessed implements ports.ProcessedEvents. Called inside a
// ports.UnitOfWork (the consumers' case) the INSERT is part of the SAME
// transaction as the event's effects, so a rollback un-claims the id. A
// concurrent claim of the same id blocks on the primary key until the other
// transaction ends.
func (r *InboxRepo) MarkProcessed(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO processed_events (event_id, event_type, processed_at)
		VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`, eventID, eventType, at)
	if err != nil {
		return false, fmt.Errorf("mark event %s processed: %w", eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}
