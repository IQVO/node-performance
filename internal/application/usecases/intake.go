package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

var (
	// ErrInvalidEvent marks a consumed event that can never be applied: a
	// required field is missing or breaks a value rule. The consumer
	// dead-letters it at once; retrying cannot help.
	ErrInvalidEvent = errors.New("invalid event")
	// ErrPurchaseOrderUnknown marks an acknowledgement or handover whose
	// purchase order facts have not arrived yet. It is transient: the producers'
	// topics race, so the consumer retries and then dead-letters (ADR 0003).
	ErrPurchaseOrderUnknown = errors.New("purchase order not known yet")
)

// Outcome is what an intake use case did.
type Outcome string

const (
	// OutcomeApplied: the event changed state.
	OutcomeApplied Outcome = "applied"
	// OutcomeDuplicate: the CloudEvents id was already in the inbox.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeIgnored: the event was acknowledged and changed nothing (ADR 0002,
	// A4 and A8).
	OutcomeIgnored Outcome = "ignored"
)

// EventRef is the envelope of a consumed CloudEvent.
type EventRef struct {
	// ID is the CloudEvents id: the inbox key and the observation's source id.
	ID string
	// Type is the full CloudEvents type.
	Type string
	// Time is the CloudEvents time of the event.
	Time time.Time
}

// Intake applies consumed events. One method per handled event type.
type Intake struct{ writer }

// NewIntake builds the intake use cases.
func NewIntake(d Deps) Intake { return Intake{writer{d}} }

// PurchaseOrderIssued is the relied-on content of PurchaseOrderIssued; the
// issue instant is Ref.Time.
type PurchaseOrderIssued struct {
	Ref            EventRef
	PoNumber       string
	NodeID         string
	RequiredShipBy time.Time
	AckBy          time.Time
}

// PurchaseOrderAcknowledged is PurchaseOrderAccepted (Rejected false, At =
// acceptedAt) or PurchaseOrderRejected (Rejected true, At = rejectedAt).
type PurchaseOrderAcknowledged struct {
	Ref      EventRef
	PoNumber string
	NodeID   string
	At       time.Time
	Rejected bool
}

// PurchaseOrderExpired is the relied-on content of PurchaseOrderExpired.
type PurchaseOrderExpired struct {
	Ref      EventRef
	PoNumber string
	NodeID   string
	AckBy    time.Time
}

// ShipmentConfirmed is the relied-on content of VendorShipmentConfirmed.
type ShipmentConfirmed struct {
	Ref            EventRef
	PoNumber       string
	NodeID         string
	RequiredShipBy time.Time
}

// PackageHandedOver is the relied-on content of PackageHandedOver.
type PackageHandedOver struct {
	Ref          EventRef
	PoNumber     string
	NodeID       string
	HandedOverAt time.Time
}

// PromiseKept is the relied-on content of PromiseKept.
type PromiseKept struct {
	Ref         EventRef
	NodeID      string
	DeliveredAt time.Time
}

// PromiseBroken is the relied-on content of PromiseBroken. At is deliveredAt;
// zero means the event carried none and Ref.Time is used (ADR 0002, A3).
type PromiseBroken struct {
	Ref    EventRef
	NodeID string
	At     time.Time
	Cause  string
}

// InventoryFeedApplied is the relied-on content of InventoryFeedApplied.
type InventoryFeedApplied struct {
	Ref       EventRef
	NodeID    string
	AppliedAt time.Time
}

// effect is the part of an intake that runs once the inbox row is new.
type effect func(ctx context.Context) (Outcome, error)

// apply is the common shape of every intake: inbox row and effect in ONE unit
// of work, retried on a concurrency conflict. A redelivery (the id is already
// in the inbox) applies nothing. A value the domain refuses becomes
// ErrInvalidEvent.
func (i Intake) apply(ctx context.Context, ref EventRef, run effect) (Outcome, error) {
	if !shared.ValidTextN(ref.ID, scorecard.MaxSourceEventID) || ref.Type == "" {
		return "", fmt.Errorf("%w: the CloudEvents id and type are required", ErrInvalidEvent)
	}
	var outcome Outcome
	err := transact(ctx, i.UoW, func(ctx context.Context) error {
		fresh, err := i.Inbox.MarkProcessed(ctx, ref.ID, ref.Type, i.now())
		if err != nil {
			return err
		}
		if !fresh {
			outcome = OutcomeDuplicate
			return nil
		}
		outcome, err = run(ctx)
		return err
	})
	if errors.Is(err, shared.ErrValidation) {
		return "", fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if err != nil {
		return "", err
	}
	return outcome, nil
}

// PurchaseOrderIssued upserts the purchase order facts; it records no
// observation (ADR 0002, A2).
func (i Intake) PurchaseOrderIssued(ctx context.Context, in PurchaseOrderIssued) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		return OutcomeApplied, i.upsertFacts(ctx, scorecard.PurchaseOrderFacts{
			PoNumber: in.PoNumber, NodeID: scorecard.NodeID(in.NodeID),
			IssuedAt: in.Ref.Time.UTC(), AckBy: in.AckBy.UTC(), RequiredShipBy: in.RequiredShipBy.UTC(),
		})
	})
}

// ShipmentConfirmed fills the purchase order facts with requiredShipBy.
func (i Intake) ShipmentConfirmed(ctx context.Context, in ShipmentConfirmed) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		return OutcomeApplied, i.upsertFacts(ctx, scorecard.PurchaseOrderFacts{
			PoNumber: in.PoNumber, NodeID: scorecard.NodeID(in.NodeID), RequiredShipBy: in.RequiredShipBy.UTC(),
		})
	})
}

func (i Intake) upsertFacts(ctx context.Context, f scorecard.PurchaseOrderFacts) error {
	if err := f.Validate(); err != nil {
		return err
	}
	return i.Facts.Upsert(ctx, f)
}

// factsOf loads the facts of a purchase order; a miss, or facts that cannot
// judge yet, is ErrPurchaseOrderUnknown.
func (i Intake) factsOf(ctx context.Context, poNumber string, canJudge func(scorecard.PurchaseOrderFacts) bool) (scorecard.PurchaseOrderFacts, error) {
	f, err := i.Facts.Get(ctx, poNumber)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && !canJudge(f)) {
		return scorecard.PurchaseOrderFacts{}, fmt.Errorf("%w: %s", ErrPurchaseOrderUnknown, poNumber)
	}
	return f, err
}

// PurchaseOrderAcknowledged records an acknowledgement observation, late when
// it fell in the last 10% of the ack window.
func (i Intake) PurchaseOrderAcknowledged(ctx context.Context, in PurchaseOrderAcknowledged) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		facts, err := i.factsOf(ctx, in.PoNumber, scorecard.PurchaseOrderFacts.CanJudgeAck)
		if err != nil {
			return "", err
		}
		kind := scorecard.KindAckAccepted
		if in.Rejected {
			kind = scorecard.KindAckRejected
		}
		o, err := scorecard.NewAcknowledgement(in.Ref.ID, kind, in.At, facts.IssuedAt, facts.AckBy)
		return i.record(ctx, in.NodeID, o, err)
	})
}

// PurchaseOrderExpired records an expiry observation (always late).
func (i Intake) PurchaseOrderExpired(ctx context.Context, in PurchaseOrderExpired) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		o, err := scorecard.NewExpiry(in.Ref.ID, in.AckBy)
		return i.record(ctx, in.NodeID, o, err)
	})
}

// PackageHandedOver records a handover observation judged against the purchase
// order's requiredShipBy.
func (i Intake) PackageHandedOver(ctx context.Context, in PackageHandedOver) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		facts, err := i.factsOf(ctx, in.PoNumber, scorecard.PurchaseOrderFacts.CanJudgeHandover)
		if err != nil {
			return "", err
		}
		o, err := scorecard.NewHandover(in.Ref.ID, in.HandedOverAt, facts.RequiredShipBy)
		return i.record(ctx, in.NodeID, o, err)
	})
}

// PromiseKept records a kept-promise observation.
func (i Intake) PromiseKept(ctx context.Context, in PromiseKept) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		o, err := scorecard.NewPromiseKept(in.Ref.ID, in.DeliveredAt)
		return i.record(ctx, in.NodeID, o, err)
	})
}

// PromiseBroken records a broken-promise observation when the cause is
// node-attributable (ADR 0002, A8); any other cause is acknowledged and ignored.
func (i Intake) PromiseBroken(ctx context.Context, in PromiseBroken) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		if !scorecard.AttributableCause(in.Cause) {
			return OutcomeIgnored, nil
		}
		at := in.At
		if at.IsZero() {
			at = in.Ref.Time
		}
		o, err := scorecard.NewPromiseBroken(in.Ref.ID, at)
		return i.record(ctx, in.NodeID, o, err)
	})
}

// InventoryFeedApplied records a feed observation.
func (i Intake) InventoryFeedApplied(ctx context.Context, in InventoryFeedApplied) (Outcome, error) {
	return i.apply(ctx, in.Ref, func(ctx context.Context) (Outcome, error) {
		o, err := scorecard.NewFeedApplied(in.Ref.ID, in.AppliedAt)
		return i.record(ctx, in.NodeID, o, err)
	})
}

// Acknowledge writes the inbox row of an event that has no effect here (ADR
// 0002, A4): VendorOnboarded, SubmissionProcessed, NodeRegistered and
// NodeOperationalProfileDeclared.
func (i Intake) Acknowledge(ctx context.Context, ref EventRef) (Outcome, error) {
	return i.apply(ctx, ref, func(context.Context) (Outcome, error) { return OutcomeIgnored, nil })
}

// record counts the observation o (or the error building it) on both scorecards
// of the node: the first observation of a node creates them (G33). A source
// event already counted is success (ADR 0002, A20).
func (i Intake) record(ctx context.Context, nodeID string, o scorecard.Observation, buildErr error) (Outcome, error) {
	if buildErr != nil {
		return "", buildErr
	}
	node, err := scorecard.NewNodeID(nodeID)
	if err != nil {
		return "", err
	}
	counted := false
	for _, w := range scorecard.Windows() {
		added, err := i.recordIn(ctx, node, w, o)
		if err != nil {
			return "", err
		}
		counted = counted || added
	}
	if !counted {
		return OutcomeDuplicate, nil
	}
	return OutcomeApplied, nil
}

// recordIn records o on the scorecard of one window, creating it when the node
// has none; it reports whether the observation was new.
func (i Intake) recordIn(ctx context.Context, node scorecard.NodeID, w scorecard.Window, o scorecard.Observation) (bool, error) {
	sc, err := i.Scorecards.Get(ctx, scorecard.ScorecardID(node, w))
	switch {
	case errors.Is(err, ports.ErrNotFound):
		sc, err = scorecard.Open(node, w, o)
	case err != nil:
		return false, err
	default:
		err = sc.RecordObservation(o)
	}
	if errors.Is(err, scorecard.ErrDuplicateObservation) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, i.Scorecards.Save(ctx, sc)
}
