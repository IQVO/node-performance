package scorecard

import (
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// Kind is the kind of an observation (ADR 0002, A2).
type Kind string

// The observation kinds: measured outcomes only.
const (
	KindAckAccepted   Kind = "ACK_ACCEPTED"
	KindAckRejected   Kind = "ACK_REJECTED"
	KindAckExpired    Kind = "ACK_EXPIRED"
	KindHandover      Kind = "HANDOVER"
	KindPromiseKept   Kind = "PROMISE_KEPT"
	KindPromiseBroken Kind = "PROMISE_BROKEN"
	KindFeedApplied   Kind = "FEED_APPLIED"
)

// MaxSourceEventID is the longest accepted CloudEvents id.
const MaxSourceEventID = 200

// lateAckDivisor makes the late part of an ack window one tenth of it (A7).
const lateAckDivisor = 10

// Observation is one measured fact counted in a scorecard, keyed by the id of
// the CloudEvents event it came from.
type Observation struct {
	// SourceEventID is the CloudEvents id of the source event.
	SourceEventID string
	Kind          Kind
	// At is the instant of the fact itself (ADR 0002, A3), in UTC.
	At time.Time
	// Late is set on acknowledgement kinds: the acknowledgement fell in the
	// last 10% of the ack window, or the order expired.
	Late bool
	// OnTime is set on a handover that was not after requiredShipBy.
	OnTime bool
}

func validKind(k Kind) bool {
	switch k {
	case KindAckAccepted, KindAckRejected, KindAckExpired, KindHandover,
		KindPromiseKept, KindPromiseBroken, KindFeedApplied:
		return true
	default:
		return false
	}
}

func (o Observation) validate() error {
	if !shared.ValidTextN(o.SourceEventID, MaxSourceEventID) {
		return shared.Invalid("source event id must be 1 to %d characters without control characters", MaxSourceEventID)
	}
	if !validKind(o.Kind) {
		return shared.Invalid("unknown observation kind %q", o.Kind)
	}
	if o.At.IsZero() {
		return shared.Invalid("observation instant is required")
	}
	return nil
}

func newObservation(sourceID string, kind Kind, at time.Time) (Observation, error) {
	o := Observation{SourceEventID: sourceID, Kind: kind, At: shared.UTC(at)}
	if err := o.validate(); err != nil {
		return Observation{}, err
	}
	return o, nil
}

// AckIsLate reports whether an acknowledgement at ackedAt falls in the last 10%
// of the ack window [issuedAt, ackBy]. An acknowledgement at or after ackBy is
// late; a window that ends before it starts counts as empty.
func AckIsLate(issuedAt, ackBy, ackedAt time.Time) bool {
	window := max(ackBy.Sub(issuedAt), 0)
	lateFrom := ackBy.Add(-window / lateAckDivisor)
	return !ackedAt.Before(lateFrom)
}

// NewAcknowledgement builds the observation of a purchase order the node
// accepted (KindAckAccepted) or rejected (KindAckRejected) at ackedAt.
func NewAcknowledgement(sourceID string, kind Kind, ackedAt, issuedAt, ackBy time.Time) (Observation, error) {
	if kind != KindAckAccepted && kind != KindAckRejected {
		return Observation{}, shared.Invalid("an acknowledgement is ACK_ACCEPTED or ACK_REJECTED, not %q", kind)
	}
	if issuedAt.IsZero() || ackBy.IsZero() {
		return Observation{}, shared.Invalid("issuedAt and ackBy are required to judge an acknowledgement")
	}
	o, err := newObservation(sourceID, kind, ackedAt)
	if err != nil {
		return Observation{}, err
	}
	o.Late = AckIsLate(issuedAt, ackBy, ackedAt)
	return o, nil
}

// NewExpiry builds the observation of a purchase order that expired at ackBy:
// always late.
func NewExpiry(sourceID string, ackBy time.Time) (Observation, error) {
	o, err := newObservation(sourceID, KindAckExpired, ackBy)
	if err != nil {
		return Observation{}, err
	}
	o.Late = true
	return o, nil
}

// NewHandover builds the observation of a package handed over at handedOverAt
// against the purchase order's requiredShipBy; the boundary instant is on time.
func NewHandover(sourceID string, handedOverAt, requiredShipBy time.Time) (Observation, error) {
	if requiredShipBy.IsZero() {
		return Observation{}, shared.Invalid("requiredShipBy is required to judge a handover")
	}
	o, err := newObservation(sourceID, KindHandover, handedOverAt)
	if err != nil {
		return Observation{}, err
	}
	o.OnTime = !handedOverAt.After(requiredShipBy)
	return o, nil
}

// NewPromiseKept builds the observation of a promise kept for the node.
func NewPromiseKept(sourceID string, at time.Time) (Observation, error) {
	return newObservation(sourceID, KindPromiseKept, at)
}

// NewPromiseBroken builds the observation of a promise broken for a
// node-attributable cause (see AttributableCause).
func NewPromiseBroken(sourceID string, at time.Time) (Observation, error) {
	return newObservation(sourceID, KindPromiseBroken, at)
}

// NewFeedApplied builds the observation of an inventory feed applied at appliedAt.
func NewFeedApplied(sourceID string, appliedAt time.Time) (Observation, error) {
	return newObservation(sourceID, KindFeedApplied, appliedAt)
}

// AttributableCause reports whether a PromiseBroken cause counts against the
// node (ADR 0002, A8): LATE and the three causes named by G33. LOST, RETURNED
// and any unknown cause do not.
func AttributableCause(cause string) bool {
	switch cause {
	case "LATE", "LATE_HANDOVER", "VENDOR_REJECT", "ACK_EXPIRED":
		return true
	default:
		return false
	}
}
