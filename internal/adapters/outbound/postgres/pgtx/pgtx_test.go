package pgtx_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres/pgtx"
)

// stubTx satisfies pgx.Tx without any behaviour: With and From only carry it.
type stubTx struct{ pgx.Tx }

func TestWithCarriesTheTransactionFromReturns(t *testing.T) {
	tx := &stubTx{}
	ctx := pgtx.With(context.Background(), tx)
	got, ok := pgtx.From(ctx)
	if !ok {
		t.Fatal("From did not find the transaction With stored")
	}
	if got != pgx.Tx(tx) {
		t.Fatalf("From = %v, want the exact transaction stored", got)
	}
}

func TestFromWithoutATransaction(t *testing.T) {
	if tx, ok := pgtx.From(context.Background()); ok || tx != nil {
		t.Fatalf("From on a plain ctx = %v, %v; want nil, false", tx, ok)
	}
}

func TestANilTransactionIsNotCarried(t *testing.T) {
	ctx := pgtx.With(context.Background(), nil)
	if tx, ok := pgtx.From(ctx); ok || tx != nil {
		t.Fatalf("From after With(nil) = %v, %v; want nil, false", tx, ok)
	}
}

func TestWithDoesNotChangeTheParentContext(t *testing.T) {
	parent := context.Background()
	pgx_tx := &stubTx{}
	_ = pgtx.With(parent, pgx_tx)
	if _, ok := pgtx.From(parent); ok {
		t.Fatal("With leaked the transaction into the parent ctx")
	}
}
