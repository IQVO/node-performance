package scorecard

import (
	"sort"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// CartonStatusReceived is the receipt carton status that counts as accurate
// (ADR 0009). Any other status, known or not, is a carton that did not arrive
// as expected.
const CartonStatusReceived = "RECEIVED"

// InboundShipment is what seller-inbound's InboundShipmentShipped says: the
// instant a shipment left towards a node.
type InboundShipment struct {
	ShipmentID string
	NodeID     NodeID
	ShippedAt  time.Time
}

// NewInboundShipment validates and normalizes a shipped fact.
func NewInboundShipment(shipmentID string, node NodeID, shippedAt time.Time) (InboundShipment, error) {
	if !shared.ValidTextN(shipmentID, MaxSourceEventID) {
		return InboundShipment{}, shared.Invalid("shipment id must be 1 to %d characters without control characters", MaxSourceEventID)
	}
	if _, err := NewNodeID(string(node)); err != nil {
		return InboundShipment{}, err
	}
	if shippedAt.IsZero() {
		return InboundShipment{}, shared.Invalid("shippedAt is required")
	}
	return InboundShipment{ShipmentID: shipmentID, NodeID: node, ShippedAt: shared.Instant(shippedAt)}, nil
}

// InboundReceipt is what vendor-connectivity's InboundReceiptSubmitted says,
// reduced to what the two metrics need: when, and how many cartons arrived as
// RECEIVED out of all reported.
type InboundReceipt struct {
	ShipmentID      string
	NodeID          NodeID
	ReceivedAt      time.Time
	CartonsTotal    int
	CartonsReceived int
}

// NewInboundReceipt builds a receipt from the statuses of its cartons.
func NewInboundReceipt(shipmentID string, node NodeID, receivedAt time.Time, statuses []string) (InboundReceipt, error) {
	if !shared.ValidTextN(shipmentID, MaxSourceEventID) {
		return InboundReceipt{}, shared.Invalid("shipment id must be 1 to %d characters without control characters", MaxSourceEventID)
	}
	if _, err := NewNodeID(string(node)); err != nil {
		return InboundReceipt{}, err
	}
	if receivedAt.IsZero() {
		return InboundReceipt{}, shared.Invalid("receivedAt is required")
	}
	r := InboundReceipt{ShipmentID: shipmentID, NodeID: node, ReceivedAt: shared.Instant(receivedAt), CartonsTotal: len(statuses)}
	for _, s := range statuses {
		if s == CartonStatusReceived {
			r.CartonsReceived++
		}
	}
	return r, nil
}

// InboundObservation is one receipt joined to its shipment: the unit both
// inbound metrics are computed from. Its instant is ReceivedAt (A3 reading).
type InboundObservation struct {
	ReceivedAt      time.Time
	Minutes         int
	CartonsTotal    int
	CartonsReceived int
}

// JoinInbound joins a receipt to the shipped instant of its shipment. The
// duration is receivedAt - shippedAt truncated to whole minutes; a receipt
// stamped before its shipment (clock skew between two producers) is CLAMPED to
// zero minutes, not rejected: the carton counts are still real facts (ADR 0009).
func JoinInbound(r InboundReceipt, shippedAt time.Time) InboundObservation {
	minutes := int(max(r.ReceivedAt.Sub(shippedAt), 0) / time.Minute)
	return InboundObservation{ReceivedAt: r.ReceivedAt, Minutes: minutes, CartonsTotal: r.CartonsTotal, CartonsReceived: r.CartonsReceived}
}

// InboundMetrics are the two informational inbound metrics of one window.
// A nil field means no data: the REST read omits it.
type InboundMetrics struct {
	// TimelinessP95Minutes: nearest-rank p95 of receivedAt - shippedAt in
	// minutes; nil without a joined receipt in the window.
	TimelinessP95Minutes *int
	// AccuracyRate: RECEIVED cartons over all cartons of the receipts in the
	// window; nil when they reported no carton. Higher is better.
	AccuracyRate *float64
}

// ComputeInbound derives the inbound metrics of window w at now from the joined
// observations of ONE node: an observation counts when receivedAt is after
// now - window (the same rule as A3), and a receipt with no cartons adds a
// timeliness sample and nothing to the accuracy.
func ComputeInbound(observations []InboundObservation, now time.Time, w Window) InboundMetrics {
	from := now.Add(-w.Duration())
	var minutes []int
	total, received := 0, 0
	for _, o := range observations {
		if !o.ReceivedAt.After(from) {
			continue
		}
		minutes = append(minutes, o.Minutes)
		total += o.CartonsTotal
		received += o.CartonsReceived
	}
	var m InboundMetrics
	if len(minutes) == 0 {
		return m
	}
	p := nearestRankP95(minutes)
	m.TimelinessP95Minutes = &p
	if total > 0 {
		a := float64(received) / float64(total)
		m.AccuracyRate = &a
	}
	return m
}

// nearestRankP95 is the ceil(0.95*n)-th smallest value; values is not empty.
func nearestRankP95(values []int) int {
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	rank := (p95Rank*len(sorted) + 99) / 100
	return sorted[rank-1]
}
