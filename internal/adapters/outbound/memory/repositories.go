package memory

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// checkVersion applies the optimistic-concurrency rule of the ports package:
// a create (version 1) needs the id to be free, an update needs the stored
// version to be version-1.
func checkVersion(stored int64, exists bool, version int64) error {
	if version == 1 {
		if exists {
			return fmt.Errorf("%w: id already stored", ports.ErrConcurrentUpdate)
		}
		return nil
	}
	if !exists || stored != version-1 {
		return fmt.Errorf("%w: stored version is not %d", ports.ErrConcurrentUpdate, version-1)
	}
	return nil
}

// sortedIDs returns the stored scorecard ids in byte order, the order of the
// Postgres adapter (COLLATE "C").
func (s *Store) sortedIDs() []string {
	ids := make([]string, 0, len(s.scorecards))
	for id := range s.scorecards {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ScorecardRepository is the in-memory ports.ScorecardRepository.
type ScorecardRepository struct{ s *Store }

// Get returns the scorecard or ports.ErrNotFound.
func (r *ScorecardRepository) Get(ctx context.Context, id string) (*scorecard.NodeScorecard, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.scorecards[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return scorecard.Restore(snap)
}

// Save stores the snapshot under the concurrency rule of the ports package.
func (r *ScorecardRepository) Save(ctx context.Context, sc *scorecard.NodeScorecard) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.scorecards[sc.ID()]
	if err := checkVersion(stored.Version, exists, sc.Version()); err != nil {
		return err
	}
	r.s.scorecards[sc.ID()] = sc.Snapshot()
	return nil
}

// ListDue returns the ids of the scorecards due at now, by id ascending.
func (r *ScorecardRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]string, error) {
	defer r.s.enter(ctx)()
	out := []string{}
	for _, id := range r.s.sortedIDs() {
		if len(out) == limit {
			break
		}
		sc, err := scorecard.Restore(r.s.scorecards[id])
		if err != nil {
			return nil, err
		}
		if sc.DueForRecompute(now) {
			out = append(out, id)
		}
	}
	return out, nil
}

// ScorecardReader is the in-memory ports.ScorecardReader.
type ScorecardReader struct{ s *Store }

// ByNode returns the views of the node's scorecards, 7d first.
func (r *ScorecardReader) ByNode(ctx context.Context, nodeID scorecard.NodeID) ([]views.Scorecard, error) {
	defer r.s.enter(ctx)()
	out := []views.Scorecard{}
	for _, w := range scorecard.Windows() {
		if snap, ok := r.s.scorecards[scorecard.ScorecardID(nodeID, w)]; ok {
			out = append(out, views.ScorecardOf(snap))
		}
	}
	return out, nil
}

// ByGrade returns one page of the scorecards with the wire grade of the filter.
func (r *ScorecardReader) ByGrade(ctx context.Context, f repository.ScorecardFilter) (views.ScorecardPage, error) {
	defer r.s.enter(ctx)()
	page := views.ScorecardPage{Items: []views.Scorecard{}}
	for _, id := range r.s.sortedIDs() {
		if f.After != "" && id <= f.After {
			continue
		}
		v := views.ScorecardOf(r.s.scorecards[id])
		if v.Grade != f.Grade || (f.Window != "" && v.Window != string(f.Window)) {
			continue
		}
		if len(page.Items) == f.Limit {
			page.NextCursor = page.Items[len(page.Items)-1].ScorecardID
			break
		}
		page.Items = append(page.Items, v)
	}
	return page, nil
}

// PurchaseOrderFacts is the in-memory ports.PurchaseOrderFactsRepository.
type PurchaseOrderFacts struct{ s *Store }

// Get returns the facts or ports.ErrNotFound.
func (r *PurchaseOrderFacts) Get(ctx context.Context, poNumber string) (scorecard.PurchaseOrderFacts, error) {
	defer r.s.enter(ctx)()
	f, ok := r.s.facts[poNumber]
	if !ok {
		return scorecard.PurchaseOrderFacts{}, ports.ErrNotFound
	}
	return f, nil
}

// Upsert merges the facts into what is stored.
func (r *PurchaseOrderFacts) Upsert(ctx context.Context, f scorecard.PurchaseOrderFacts) error {
	defer r.s.enter(ctx)()
	if err := f.Validate(); err != nil {
		return err
	}
	if stored, ok := r.s.facts[f.PoNumber]; ok {
		f = stored.Merge(f)
	}
	r.s.facts[f.PoNumber] = f
	return nil
}

// Inbox is the in-memory ports.ProcessedEvents.
type Inbox struct{ s *Store }

// MarkProcessed records the event id and reports whether it is new.
func (i *Inbox) MarkProcessed(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.processed[eventID]; seen {
		return false, nil
	}
	i.s.processed[eventID] = processedRow{eventType: eventType, at: at}
	return true, nil
}

// Inbound is the in-memory ports.InboundRepository and ports.InboundReader.
type Inbound struct{ s *Store }

// SaveShipment stores the shipped instant; the first one wins.
func (r *Inbound) SaveShipment(ctx context.Context, sh scorecard.InboundShipment) (bool, error) {
	defer r.s.enter(ctx)()
	if _, ok := r.s.shipments[sh.ShipmentID]; ok {
		return false, nil
	}
	r.s.shipments[sh.ShipmentID] = sh
	return true, nil
}

// SaveReceipt stores the receipt; the first receipt of a shipment wins.
func (r *Inbound) SaveReceipt(ctx context.Context, rc scorecard.InboundReceipt) (bool, error) {
	defer r.s.enter(ctx)()
	if _, ok := r.s.receipts[rc.ShipmentID]; ok {
		return false, nil
	}
	r.s.receipts[rc.ShipmentID] = rc
	return true, nil
}

// Observations joins the stored receipts to their shipments.
func (r *Inbound) Observations(ctx context.Context, nodes []scorecard.NodeID, after time.Time) (map[scorecard.NodeID][]scorecard.InboundObservation, error) {
	defer r.s.enter(ctx)()
	want := map[scorecard.NodeID]bool{}
	for _, n := range nodes {
		want[n] = true
	}
	out := map[scorecard.NodeID][]scorecard.InboundObservation{}
	ids := make([]string, 0, len(r.s.receipts))
	for id := range r.s.receipts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		rc := r.s.receipts[id]
		sh, joined := r.s.shipments[id]
		if !want[rc.NodeID] || !joined || !rc.ReceivedAt.After(after) {
			continue
		}
		out[rc.NodeID] = append(out[rc.NodeID], scorecard.JoinInbound(rc, sh.ShippedAt))
	}
	return out, nil
}
