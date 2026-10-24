// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so
// a loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store
// for the whole unit and restores the state taken at the start when the unit
// fails or panics, outbox and inbox included.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// processedRow is one inbox row.
type processedRow struct {
	eventType string
	at        time.Time
}

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu         sync.Mutex
	scorecards map[string]scorecard.Snapshot
	facts      map[string]scorecard.PurchaseOrderFacts
	shipments  map[string]scorecard.InboundShipment
	receipts   map[string]scorecard.InboundReceipt
	processed  map[string]processedRow
	outbox     []shared.Event
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		scorecards: map[string]scorecard.Snapshot{},
		facts:      map[string]scorecard.PurchaseOrderFacts{},
		shipments:  map[string]scorecard.InboundShipment{},
		receipts:   map[string]scorecard.InboundReceipt{},
		processed:  map[string]processedRow{},
	}
}

// state is a copy of everything a unit of work can change. Stored snapshots
// are replaced, never edited in place, so copying the maps is enough.
type state struct {
	scorecards map[string]scorecard.Snapshot
	facts      map[string]scorecard.PurchaseOrderFacts
	shipments  map[string]scorecard.InboundShipment
	receipts   map[string]scorecard.InboundReceipt
	processed  map[string]processedRow
	outbox     []shared.Event
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) capture() state {
	return state{
		scorecards: cloneMap(s.scorecards), facts: cloneMap(s.facts), shipments: cloneMap(s.shipments), receipts: cloneMap(s.receipts), processed: cloneMap(s.processed),
		outbox: append([]shared.Event(nil), s.outbox...),
	}
}

func (s *Store) restore(st state) {
	s.scorecards, s.facts, s.processed, s.outbox = st.scorecards, st.facts, st.processed, st.outbox
	s.shipments, s.receipts = st.shipments, st.receipts
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Scorecards returns the ScorecardRepository double.
func (s *Store) Scorecards() *ScorecardRepository { return &ScorecardRepository{s} }

// Reader returns the ScorecardReader double.
func (s *Store) Reader() *ScorecardReader { return &ScorecardReader{s} }

// Facts returns the PurchaseOrderFactsRepository double.
func (s *Store) Facts() *PurchaseOrderFacts { return &PurchaseOrderFacts{s} }

// Inbound returns the InboundRepository and InboundReader double.
func (s *Store) Inbound() *Inbound { return &Inbound{s} }

// Inbox returns the ProcessedEvents double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	Scorecards, Facts, Processed, OutboxEvents, Shipments, Receipts int
	// VersionSum is the sum of every scorecard version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the scorecard
// versions, so a test can prove that a failed use case stored and changed
// nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var versions int64
	for _, sc := range s.scorecards {
		versions += sc.Version
	}
	return Counts{
		Scorecards: len(s.scorecards), Facts: len(s.facts), Processed: len(s.processed),
		OutboxEvents: len(s.outbox), Shipments: len(s.shipments), Receipts: len(s.receipts), VersionSum: versions,
	}
}
