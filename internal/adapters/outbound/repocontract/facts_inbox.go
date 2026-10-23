package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

var factsCases = []named{
	{"a miss is ports.ErrNotFound", factsMiss},
	{"an upsert creates and round-trips the facts", factsCreate},
	{"an upsert only fills: a stored instant is never overwritten or erased", factsMerge},
	{"facts that fail validation are refused", factsInvalid},
}

var inboxCases = []named{
	{"the first mark of an id is new, a repeat is not", inboxRepeat},
	{"the inbox is keyed by the id alone", inboxKeyedByID},
	{"a mark rolls back with its unit of work", inboxRollsBack},
}

func factsMiss(t *testing.T, f Fixture) {
	_, err := f.Facts.Get(context.Background(), "po-ghost")
	wantErr(t, err, ports.ErrNotFound)
}

func getFacts(t *testing.T, f Fixture, po string) scorecard.PurchaseOrderFacts {
	t.Helper()
	got, err := f.Facts.Get(context.Background(), po)
	noErr(t, err)
	return got
}

func factsCreate(t *testing.T, f Fixture) {
	want := scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1", IssuedAt: At(1), AckBy: At(2), RequiredShipBy: At(3)}
	noErr(t, f.Facts.Upsert(context.Background(), want))
	if got := getFacts(t, f, "po-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("facts = %+v, want %+v", got, want)
	}
	partial := scorecard.PurchaseOrderFacts{PoNumber: "po-2", NodeID: "node-1", RequiredShipBy: At(3)}
	noErr(t, f.Facts.Upsert(context.Background(), partial))
	if got := getFacts(t, f, "po-2"); !reflect.DeepEqual(got, partial) || !got.IssuedAt.IsZero() || !got.AckBy.IsZero() {
		t.Fatalf("a missing instant stays zero: %+v", got)
	}
}

func factsMerge(t *testing.T, f Fixture) {
	ctx := context.Background()
	noErr(t, f.Facts.Upsert(ctx, scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1", RequiredShipBy: At(3)}))
	noErr(t, f.Facts.Upsert(ctx, scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1", IssuedAt: At(1), AckBy: At(2), RequiredShipBy: At(99)}))
	want := scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1", IssuedAt: At(1), AckBy: At(2), RequiredShipBy: At(3)}
	if got := getFacts(t, f, "po-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the second upsert: %+v, want %+v", got, want)
	}
	noErr(t, f.Facts.Upsert(ctx, scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1"}))
	if got := getFacts(t, f, "po-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("an upsert without instants erased something: %+v", got)
	}
}

func factsInvalid(t *testing.T, f Fixture) {
	wantErr(t, f.Facts.Upsert(context.Background(), scorecard.PurchaseOrderFacts{PoNumber: "", NodeID: "node-1"}), shared.ErrValidation)
	wantErr(t, f.Facts.Upsert(context.Background(), scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "a:b"}), shared.ErrValidation)
}

func mark(t *testing.T, f Fixture, ctx context.Context, id, typ string) bool {
	t.Helper()
	fresh, err := f.Inbox.MarkProcessed(ctx, id, typ, Now)
	noErr(t, err)
	return fresh
}

func inboxRepeat(t *testing.T, f Fixture) {
	ctx := context.Background()
	if !mark(t, f, ctx, "evt-1", "com.example.A") {
		t.Fatal("the first mark must be new")
	}
	if mark(t, f, ctx, "evt-1", "com.example.A") {
		t.Fatal("a repeat must not be new")
	}
}

func inboxKeyedByID(t *testing.T, f Fixture) {
	ctx := context.Background()
	mark(t, f, ctx, "evt-1", "com.example.A")
	if mark(t, f, ctx, "evt-1", "com.example.B") {
		t.Fatal("the same id under another type is still a repeat")
	}
	if !mark(t, f, ctx, "evt-2", "com.example.A") {
		t.Fatal("another id is new")
	}
}

func inboxRollsBack(t *testing.T, f Fixture) {
	err := f.UoW.Do(context.Background(), func(ctx context.Context) error {
		if !mark(t, f, ctx, "evt-1", "com.example.A") {
			t.Fatal("new inside the unit")
		}
		return errBoom
	})
	wantErr(t, err, errBoom)
	if !mark(t, f, context.Background(), "evt-1", "com.example.A") {
		t.Fatal("a rolled-back mark must be new again")
	}
}
