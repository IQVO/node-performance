package scorecard

import (
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// PurchaseOrderFacts is what the producers said about one purchase order and
// the judgement of an acknowledgement or a handover needs (ADR 0003, decision
// 3). A zero instant means "not seen yet".
type PurchaseOrderFacts struct {
	PoNumber       string
	NodeID         NodeID
	IssuedAt       time.Time
	AckBy          time.Time
	RequiredShipBy time.Time
}

// Validate checks the identity of the facts.
func (f PurchaseOrderFacts) Validate() error {
	if !shared.ValidTextN(f.PoNumber, shared.MaxID) {
		return shared.Invalid("poNumber must be 1 to %d characters without control characters", shared.MaxID)
	}
	_, err := NewNodeID(string(f.NodeID))
	return err
}

// Normalized returns f with every instant as the adapters store it
// (shared.Instant: UTC, microsecond precision).
func (f PurchaseOrderFacts) Normalized() PurchaseOrderFacts {
	f.IssuedAt = shared.Instant(f.IssuedAt)
	f.AckBy = shared.Instant(f.AckBy)
	f.RequiredShipBy = shared.Instant(f.RequiredShipBy)
	return f
}

// Merge returns f filled with the instants of newer that f does not have yet: a
// field is only ever filled, never erased or overwritten.
func (f PurchaseOrderFacts) Merge(newer PurchaseOrderFacts) PurchaseOrderFacts {
	if f.IssuedAt.IsZero() {
		f.IssuedAt = newer.IssuedAt
	}
	if f.AckBy.IsZero() {
		f.AckBy = newer.AckBy
	}
	if f.RequiredShipBy.IsZero() {
		f.RequiredShipBy = newer.RequiredShipBy
	}
	return f
}

// CanJudgeAck reports whether an acceptance or rejection can be judged.
func (f PurchaseOrderFacts) CanJudgeAck() bool { return !f.IssuedAt.IsZero() && !f.AckBy.IsZero() }

// CanJudgeHandover reports whether a handover can be judged.
func (f PurchaseOrderFacts) CanJudgeHandover() bool { return !f.RequiredShipBy.IsZero() }
