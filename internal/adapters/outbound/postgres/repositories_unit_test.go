package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/node-performance/internal/application/outbox"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// failingEncoder is an outbox.Encoder whose Encode always fails with cause.
type failingEncoder struct{ cause error }

func (e failingEncoder) Encode(shared.Event) (outbox.Message, error) {
	return outbox.Message{}, e.cause
}

func TestConstructorsBuildOverThePool(t *testing.T) {
	if NewScorecardRepo(nil) == nil {
		t.Fatal("NewScorecardRepo(nil) = nil")
	}
	if NewPurchaseOrderFactsRepo(nil) == nil {
		t.Fatal("NewPurchaseOrderFactsRepo(nil) = nil")
	}
	if NewInboxRepo(nil) == nil {
		t.Fatal("NewInboxRepo(nil) = nil")
	}
	if NewReader(nil) == nil {
		t.Fatal("NewReader(nil) = nil")
	}
	if NewRelayStore(nil) == nil {
		t.Fatal("NewRelayStore(nil) = nil")
	}
	if NewUnitOfWork(nil) == nil {
		t.Fatal("NewUnitOfWork(nil) = nil")
	}
}

func TestOutboxAppendWithoutEventsWritesNothing(t *testing.T) {
	// A nil encoder and a nil pool are both fine: an empty batch returns
	// before either is touched.
	if err := NewOutboxRepo(nil, nil).Append(context.Background()); err != nil {
		t.Fatalf("Append() = %v, want nil", err)
	}
}

func TestOutboxAppendWithoutEncoderIsAWiringError(t *testing.T) {
	err := NewOutboxRepo(nil, nil).Append(context.Background(), scorecard.NodeScorecardUpdated{})
	if !errors.Is(err, ErrNilEncoder) {
		t.Fatalf("Append without an encoder = %v, want ErrNilEncoder", err)
	}
}

func TestOutboxAppendEncodesEveryEventBeforeWriting(t *testing.T) {
	// The encode failure must surface before any statement runs, wrapped
	// with the event name, whatever the pool (nil here) would say.
	cause := errors.New("cannot encode")
	repo := NewOutboxRepo(nil, failingEncoder{cause: cause})
	err := repo.Append(context.Background(), scorecard.NodeScorecardUpdated{NodeID: "N-1", Window: scorecard.Window30d})
	if !errors.Is(err, cause) {
		t.Fatalf("Append = %v, want it to wrap %v", err, cause)
	}
	if !strings.Contains(err.Error(), "NodeScorecardUpdated") {
		t.Fatalf("Append = %v, want it to name the event", err)
	}
}

func TestFactsUpsertValidatesBeforeItWrites(t *testing.T) {
	cases := []struct {
		name string
		f    scorecard.PurchaseOrderFacts
	}{
		{"no po number", scorecard.PurchaseOrderFacts{NodeID: "N-1"}},
		{"no node", scorecard.PurchaseOrderFacts{PoNumber: "PO-1"}},
		{"node with the scorecard separator", scorecard.PurchaseOrderFacts{PoNumber: "PO-1", NodeID: "N:1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewPurchaseOrderFactsRepo(nil).Upsert(context.Background(), tc.f)
			if !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("Upsert(%+v) = %v, want a validation error", tc.f, err)
			}
		})
	}
}

func TestNewMigratorRejectsADatabaseURLItCannotOpen(t *testing.T) {
	if _, err := NewMigrator("://not-a-url"); err == nil {
		t.Fatal("NewMigrator accepted an unopenable URL")
	}
}

func TestNewPoolRejectsADatabaseURLItCannotParse(t *testing.T) {
	if _, err := NewPool(context.Background(), "://not-a-url"); err == nil {
		t.Fatal("NewPool accepted an unparseable URL")
	}
}

func TestUnitOfWorkJoinsTheTransactionTheContextCarries(t *testing.T) {
	// A ctx that already carries a transaction makes Do a pass-through: fn
	// runs on the same ctx (nil pool, nothing began) and its error is
	// returned as is.
	tx := &carriedTx{}
	ctx := pgtx.With(context.Background(), tx)
	var seen context.Context
	uow := NewUnitOfWork(nil)
	if err := uow.Do(ctx, func(c context.Context) error {
		seen = c
		return nil
	}); err != nil {
		t.Fatalf("Do on a carried tx = %v, want nil", err)
	}
	if seen != ctx {
		t.Fatal("Do handed fn a different ctx than the one carrying the tx")
	}
	cause := errors.New("fn failed")
	if err := uow.Do(ctx, func(context.Context) error { return cause }); !errors.Is(err, cause) {
		t.Fatalf("Do returned %v, want fn's error unchanged", err)
	}
}
