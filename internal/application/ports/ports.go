// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// two sentinels of errors.go, the filters of internal/application/repository
// and the read DTOs of internal/application/views.
//
// Optimistic concurrency: every Save expects the stored version to be
// aggregate.Version()-1 (ADR 0002); a create (Version() 1) expects that the id
// is not stored. Any mismatch, including an update of a row that no longer
// exists, is ErrConcurrentUpdate. Inside a UnitOfWork every repository joins
// the transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// ScorecardRepository persists NodeScorecard aggregates, observations
// included.
type ScorecardRepository interface {
	// Get returns the scorecard with every observation it holds, or
	// ErrNotFound. The id is the scorecardId (nodeId:window).
	Get(ctx context.Context, id string) (*scorecard.NodeScorecard, error)
	// Save persists the scorecard snapshot under the concurrency rule of the
	// package comment, replacing the stored observation set with the
	// snapshot's.
	Save(ctx context.Context, s *scorecard.NodeScorecard) error
	// ListDue returns the ids of at most limit scorecards due for a recompute
	// at now (scorecard.DueForRecompute), ordered by id ascending.
	ListDue(ctx context.Context, now time.Time, limit int) ([]string, error)
}

// ScorecardReader is the read side of the two REST queries. It returns views
// straight from the scorecards, without their observations.
type ScorecardReader interface {
	// ByNode returns the scorecards of the node, 7d first; none is an empty
	// list, never an error.
	ByNode(ctx context.Context, nodeID scorecard.NodeID) ([]views.Scorecard, error)
	// ByGrade returns one page of the scorecards matching the filter,
	// ordered by scorecardId ascending.
	ByGrade(ctx context.Context, filter repository.ScorecardFilter) (views.ScorecardPage, error)
}

// PurchaseOrderFactsRepository keeps what the producers said about purchase
// orders (ADR 0003, decision 3).
type PurchaseOrderFactsRepository interface {
	// Get returns the facts of the purchase order, or ErrNotFound.
	Get(ctx context.Context, poNumber string) (scorecard.PurchaseOrderFacts, error)
	// Upsert stores the facts, merging them into what is stored: a stored
	// instant is never overwritten or erased (PurchaseOrderFacts.Merge).
	Upsert(ctx context.Context, facts scorecard.PurchaseOrderFacts) error
}

// ProcessedEvents is the inbox: one row per consumed CloudEvents id.
type ProcessedEvents interface {
	// MarkProcessed records the event id and reports whether it is new. A
	// false result means a redelivery: the caller applies nothing. Inside a
	// UnitOfWork the row commits or rolls back with the effect.
	MarkProcessed(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls, so the aggregates and their raised events commit or roll back
// together. The adapter encodes each event into its CloudEvents envelope (the
// id is minted once, at append) and stores one row per event in order; a relay
// drains the rows to Kafka afterwards. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics).
//
//   - fn MUST use the ctx passed to it for every call that should participate;
//     the unit travels in that ctx.
//   - Do is re-entrant: when ctx already carries an open unit, Do joins it and
//     the outermost Do owns commit and rollback.
//   - The error fn returns is returned from Do unchanged (errors.Is keeps
//     working); a commit failure is returned wrapped.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// InboundRepository keeps the two local read models of the inbound metrics
// (ADR 0007): the shipped instant of each shipment (from seller-inbound) and
// the carton counts of each receipt (from vendor-connectivity). Both are
// keyed by shipmentId and the first fact stored wins, so a redelivery or a
// second report never moves a stored value and a receipt is counted once. A
// receipt that arrives before its shipment simply stays stored until the
// shipment arrives; the read side joins the two.
type InboundRepository interface {
	// SaveShipment stores the shipped instant and reports whether it was new.
	SaveShipment(ctx context.Context, s scorecard.InboundShipment) (bool, error)
	// SaveReceipt stores the receipt and reports whether it was new (the first
	// receipt of a shipment wins).
	SaveReceipt(ctx context.Context, r scorecard.InboundReceipt) (bool, error)
}

// InboundReader is the read side of the inbound metrics.
type InboundReader interface {
	// Observations returns, per node, the receipts received after `after` that
	// are already joined to their shipment. Parked receipts (no shipment yet)
	// are not returned. Nodes without data are absent from the map.
	Observations(ctx context.Context, nodes []scorecard.NodeID, after time.Time) (map[scorecard.NodeID][]scorecard.InboundObservation, error)
}
