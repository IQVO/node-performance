package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// PurchaseOrderFactsRepo is the Postgres ports.PurchaseOrderFactsRepository.
type PurchaseOrderFactsRepo struct{ pool *pgxpool.Pool }

var _ ports.PurchaseOrderFactsRepository = (*PurchaseOrderFactsRepo)(nil)

// NewPurchaseOrderFactsRepo constructs the repository over pool.
func NewPurchaseOrderFactsRepo(pool *pgxpool.Pool) *PurchaseOrderFactsRepo {
	return &PurchaseOrderFactsRepo{pool: pool}
}

// Get implements ports.PurchaseOrderFactsRepository.
func (r *PurchaseOrderFactsRepo) Get(ctx context.Context, poNumber string) (scorecard.PurchaseOrderFacts, error) {
	var (
		f                  scorecard.PurchaseOrderFacts
		node               string
		issued, ack, reqBy *time.Time
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT po_number, node_id, issued_at, ack_by, required_ship_by
		FROM purchase_order_facts WHERE po_number = $1`, poNumber).Scan(&f.PoNumber, &node, &issued, &ack, &reqBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return scorecard.PurchaseOrderFacts{}, ports.ErrNotFound
	}
	if err != nil {
		return scorecard.PurchaseOrderFacts{}, fmt.Errorf("read purchase order facts %s: %w", poNumber, err)
	}
	f.NodeID = scorecard.NodeID(node)
	f.IssuedAt, f.AckBy, f.RequiredShipBy = instant(issued), instant(ack), instant(reqBy)
	return f, nil
}

func instant(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return shared.Instant(*t)
}

func nullable(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// Upsert implements ports.PurchaseOrderFactsRepository: a stored instant wins
// over a new one (COALESCE), so facts only ever fill.
func (r *PurchaseOrderFactsRepo) Upsert(ctx context.Context, f scorecard.PurchaseOrderFacts) error {
	if err := f.Validate(); err != nil {
		return err
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO purchase_order_facts (po_number, node_id, issued_at, ack_by, required_ship_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (po_number) DO UPDATE SET
			issued_at = COALESCE(purchase_order_facts.issued_at, EXCLUDED.issued_at),
			ack_by = COALESCE(purchase_order_facts.ack_by, EXCLUDED.ack_by),
			required_ship_by = COALESCE(purchase_order_facts.required_ship_by, EXCLUDED.required_ship_by)`,
		f.PoNumber, string(f.NodeID), nullable(f.IssuedAt), nullable(f.AckBy), nullable(f.RequiredShipBy))
	if err != nil {
		return fmt.Errorf("upsert purchase order facts %s: %w", f.PoNumber, err)
	}
	return nil
}
