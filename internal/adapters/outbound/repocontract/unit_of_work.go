package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

var unitOfWorkCases = []named{
	{"a returned nil commits every save", uowCommit},
	{"a returned error rolls every save back and is returned unchanged", uowRollback},
	{"a panic rolls back and propagates", uowPanic},
	{"a nested Do joins the outer unit", uowNested},
	{"the outbox commits and rolls back with the aggregates", uowOutbox},
}

var errBoom = errors.New("boom")

// saveAll stores a scorecard, facts and an inbox row through ctx.
func saveAll(t *testing.T, f Fixture, ctx context.Context) error {
	t.Helper()
	if err := f.Scorecards.Save(ctx, NewScorecard(t, "node-1", scorecard.Window7d)); err != nil {
		return err
	}
	if err := f.Facts.Upsert(ctx, scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1", RequiredShipBy: At(3)}); err != nil {
		return err
	}
	_, err := f.Inbox.MarkProcessed(ctx, "evt-1", "com.example.A", Now)
	return err
}

// storedAnything reports whether any of the three tables holds the rows saveAll writes.
func storedAnything(t *testing.T, f Fixture) bool {
	t.Helper()
	ctx := context.Background()
	if _, err := f.Scorecards.Get(ctx, "node-1:7d"); err == nil {
		return true
	}
	if _, err := f.Facts.Get(ctx, "po-1"); err == nil {
		return true
	}
	fresh, err := f.Inbox.MarkProcessed(ctx, "evt-1", "com.example.A", Now)
	noErr(t, err)
	return !fresh
}

func uowCommit(t *testing.T, f Fixture) {
	noErr(t, f.UoW.Do(context.Background(), func(ctx context.Context) error { return saveAll(t, f, ctx) }))
	ctx := context.Background()
	if _, err := f.Scorecards.Get(ctx, "node-1:7d"); err != nil {
		t.Fatalf("a committed unit lost the scorecard: %v", err)
	}
	if _, err := f.Facts.Get(ctx, "po-1"); err != nil {
		t.Fatalf("a committed unit lost the facts: %v", err)
	}
	if mark(t, f, ctx, "evt-1", "com.example.A") {
		t.Fatal("a committed unit lost the inbox row")
	}
}

func uowRollback(t *testing.T, f Fixture) {
	err := f.UoW.Do(context.Background(), func(ctx context.Context) error {
		noErr(t, saveAll(t, f, ctx))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if storedAnything(t, f) {
		t.Fatal("a failed unit left a write behind")
	}
}

func uowPanic(t *testing.T, f Fixture) {
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed")
		}
		if storedAnything(t, f) {
			t.Fatal("a panicking unit left a write behind")
		}
	}()
	_ = f.UoW.Do(context.Background(), func(ctx context.Context) error {
		noErr(t, saveAll(t, f, ctx))
		panic("kaboom")
	})
}

func uowNested(t *testing.T, f Fixture) {
	err := f.UoW.Do(context.Background(), func(ctx context.Context) error {
		noErr(t, f.UoW.Do(ctx, func(ctx context.Context) error { return saveAll(t, f, ctx) }))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if storedAnything(t, f) {
		t.Fatal("the inner unit committed on its own")
	}
}

func uowOutbox(t *testing.T, f Fixture) {
	if f.OutboxCount == nil {
		t.Skip("the fixture cannot count its outbox")
	}
	ctx := context.Background()
	events := RaisedEvents(t)
	failed := f.UoW.Do(ctx, func(ctx context.Context) error {
		noErr(t, f.Outbox.Append(ctx, events...))
		return errBoom
	})
	wantErr(t, failed, errBoom)
	assertOutbox(t, f, 0)
	noErr(t, f.UoW.Do(ctx, func(ctx context.Context) error { return f.Outbox.Append(ctx, events...) }))
	assertOutbox(t, f, len(events))
}

func assertOutbox(t *testing.T, f Fixture, want int) {
	t.Helper()
	got, err := f.OutboxCount(context.Background())
	noErr(t, err)
	if got != want {
		t.Fatalf("outbox holds %d events, want %d", got, want)
	}
}
