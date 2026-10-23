package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// newFixture wires every double over one store.
func newFixture(s *memory.Store) repocontract.Fixture {
	return repocontract.Fixture{
		Scorecards: s.Scorecards(), Reader: s.Reader(), Facts: s.Facts(), Inbox: s.Inbox(),
		Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		OutboxCount: func(context.Context) (int, error) { return len(s.Outbox().Events()), nil },
	}
}

// TestRepositoryContract runs the repository contract against the doubles.
func TestRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(*testing.T) repocontract.Fixture { return newFixture(memory.NewStore()) })
}

func TestClock(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	c := memory.NewClock(start)
	c.Advance(time.Hour)
	if !c.Now().Equal(start.Add(time.Hour)) {
		t.Fatalf("now = %v", c.Now())
	}
	c.Set(start)
	if !c.Now().Equal(start) {
		t.Fatalf("now = %v", c.Now())
	}
}

func TestCountsFollowTheStore(t *testing.T) {
	s := memory.NewStore()
	if c := s.Counts(); c != (memory.Counts{}) {
		t.Fatalf("empty store counts = %+v", c)
	}
	f := newFixture(s)
	repocontract.Seed(t, f, "node-1", scorecard.Window7d, 2)
	ctx := context.Background()
	if err := f.Facts.Upsert(ctx, scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Inbox.MarkProcessed(ctx, "evt-1", "t", repocontract.Now); err != nil {
		t.Fatal(err)
	}
	if err := f.Outbox.Append(ctx, repocontract.RaisedEvents(t)...); err != nil {
		t.Fatal(err)
	}
	want := memory.Counts{Scorecards: 1, Facts: 1, Processed: 1, OutboxEvents: 3, VersionSum: 2}
	if c := s.Counts(); c != want {
		t.Fatalf("counts = %+v, want %+v", c, want)
	}
}

// TestStoredStateNeverAliasesALoadedAggregate: mutating a loaded scorecard does
// not change what is stored until it is saved.
func TestStoredStateNeverAliasesALoadedAggregate(t *testing.T) {
	s := memory.NewStore()
	f := newFixture(s)
	repocontract.Seed(t, f, "node-1", scorecard.Window7d, 1)
	loaded, err := f.Scorecards.Get(context.Background(), "node-1:7d")
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.RecordObservation(repocontract.Feed(t, "x", 5)); err != nil {
		t.Fatal(err)
	}
	again, err := f.Scorecards.Get(context.Background(), "node-1:7d")
	if err != nil {
		t.Fatal(err)
	}
	if again.ObservationCount() != 1 || again.Version() != 1 {
		t.Fatalf("an unsaved change leaked into the store: %d observations, version %d", again.ObservationCount(), again.Version())
	}
}
