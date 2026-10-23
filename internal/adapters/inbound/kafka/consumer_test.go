package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

func counted(t *testing.T, r *rig, id string) int {
	t.Helper()
	s, err := r.store.Scorecards().Get(context.Background(), id)
	if err != nil {
		t.Fatalf("scorecard %s: %v", id, err)
	}
	return s.ObservationCount()
}

func TestEveryConsumedTypeHasItsEffect(t *testing.T) {
	r := newRig()
	vendor := r.consumer(VendorConnectivityTopic, VendorHandlers)
	shipping := r.consumer(ShippingAndTrackingTopic, ShippingHandlers)
	promise := r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers)
	offer := r.consumer(OfferAvailabilityTopic, OfferAvailabilityHandlers)
	topology := r.consumer(NodeTopologyTopic, NodeTopologyHandlers)
	ctx := context.Background()

	po := map[string]any{"poNumber": "po-1", "nodeId": "n1", "requiredShipBy": at(1800), "ackBy": at(600)}
	steps := []struct {
		c    *Consumer
		id   string
		typ  string
		data any
	}{
		{vendor, "e1", TypePurchaseOrderIssued, po},
		{vendor, "e2", TypePurchaseOrderAccepted, map[string]any{"poNumber": "po-1", "nodeId": "n1", "acceptedAt": at(100)}},
		{vendor, "e3", TypeVendorShipmentConfirmed, map[string]any{"poNumber": "po-1", "nodeId": "n1", "requiredShipBy": at(1800)}},
		{shipping, "e4", TypePackageHandedOver, map[string]any{"poNumber": "po-1", "nodeId": "n1", "handedOverAt": at(1700)}},
		{promise, "e5", TypePromiseKept, map[string]any{"nodeId": "n1", "deliveredAt": at(5)}},
		{promise, "e6", TypePromiseBroken, map[string]any{"nodeId": "n1", "deliveredAt": at(6), "cause": "LATE"}},
		{promise, "e7", TypePromiseBroken, map[string]any{"nodeId": "n1", "cause": "LOST"}},
		{offer, "e8", TypeInventoryFeedApplied, map[string]any{"nodeId": "n1", "appliedAt": at(7)}},
	}
	for _, s := range steps {
		if err := s.c.HandleMessage(ctx, event(t, s.id, s.typ, s.data)); err != nil {
			t.Fatalf("%s: %v", s.typ, err)
		}
	}
	// accepted, handover, kept, broken (LATE), feed; LOST is not attributable
	if got := counted(t, r, "n1:30d"); got != 5 {
		t.Fatalf("observations = %d, want 5", got)
	}
	// a second node through the rejected and expired paths
	for _, s := range []struct {
		id, typ string
		data    any
	}{
		{"f1", TypePurchaseOrderIssued, map[string]any{"poNumber": "po-2", "nodeId": "n2", "requiredShipBy": at(1800), "ackBy": at(600)}},
		{"f2", TypePurchaseOrderRejected, map[string]any{"poNumber": "po-2", "nodeId": "n2", "rejectedAt": at(50), "reasonCode": "X"}},
		{"f3", TypePurchaseOrderExpired, map[string]any{"poNumber": "po-2", "nodeId": "n2", "ackBy": at(600)}},
	} {
		if err := vendor.HandleMessage(ctx, event(t, s.id, s.typ, s.data)); err != nil {
			t.Fatalf("%s: %v", s.typ, err)
		}
	}
	if got := counted(t, r, "n2:7d"); got != 2 {
		t.Fatalf("n2 observations = %d, want 2", got)
	}
	// the four no-effect types only reach the inbox
	before := r.store.Counts()
	for i, c := range []struct {
		c   *Consumer
		typ string
	}{{vendor, TypeVendorOnboarded}, {vendor, TypeSubmissionProcessed}, {topology, TypeNodeRegistered}, {topology, TypeNodeOperationalProfileDeclared}} {
		if err := c.c.HandleMessage(ctx, event(t, "n"+string(rune('a'+i)), c.typ, map[string]any{"nodeId": "n1", "vendorId": "v"})); err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
	}
	after := r.store.Counts()
	if after.Processed != before.Processed+4 || after.Scorecards != before.Scorecards || after.VersionSum != before.VersionSum || after.OutboxEvents != before.OutboxEvents {
		t.Fatalf("no-effect events changed state: %+v -> %+v", before, after)
	}
}

func TestARedeliveredEventIsCountedOnce(t *testing.T) {
	r := newRig()
	c := r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers)
	msg := event(t, "dup", TypePromiseKept, map[string]any{"nodeId": "n1", "deliveredAt": at(5)})
	for range 3 {
		if err := c.HandleMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if got := counted(t, r, "n1:30d"); got != 1 {
		t.Fatalf("observations = %d, want 1", got)
	}
	if !strings.Contains(r.logs.String(), "outcome=duplicate") {
		t.Fatalf("the duplicate outcome is logged:\n%s", r.logs.String())
	}
}

func TestWhatIsNotForUsIsSkipped(t *testing.T) {
	r := newRig()
	c := r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers)
	ctx := context.Background()
	for name, value := range map[string][]byte{
		"not json":           []byte("nope"),
		"flat legacy format": []byte(`{"event_id":"x","event_type":"PromiseKept"}`),
		"unhandled type":     event(t, "u1", "com.retailnetwork.commerce.delivery-promise.promise.PromiseCreated", map[string]any{}),
	} {
		if err := c.HandleMessage(ctx, value); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := r.store.Counts(); got.Processed != 0 || got.Scorecards != 0 {
		t.Fatalf("skipped messages change nothing: %+v", got)
	}
}

func TestAnEventThatCanNeverBeAppliedIsPoison(t *testing.T) {
	r := newRig()
	c := r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers)
	for name, value := range map[string][]byte{
		"payload of the wrong shape": event(t, "p1", TypePromiseKept, []byte(`{"nodeId": 12}`)),
		"missing node":               event(t, "p2", TypePromiseKept, map[string]any{"deliveredAt": at(1)}),
		"missing instant":            event(t, "p3", TypePromiseKept, map[string]any{"nodeId": "n1"}),
	} {
		err := c.HandleMessage(context.Background(), value)
		if !errors.Is(err, errPoison) {
			t.Errorf("%s: err = %v, want poison", name, err)
		}
	}
	if got := r.store.Counts(); got.Processed != 0 || got.Scorecards != 0 {
		t.Fatalf("a poison message changes nothing: %+v", got)
	}
}

func TestAnAcknowledgementBeforeItsIssueIsTransient(t *testing.T) {
	r := newRig()
	c := r.consumer(ShippingAndTrackingTopic, ShippingHandlers)
	err := c.HandleMessage(context.Background(), event(t, "h1", TypePackageHandedOver, map[string]any{"poNumber": "po-x", "nodeId": "n1", "handedOverAt": at(1)}))
	if !errors.Is(err, usecases.ErrPurchaseOrderUnknown) || errors.Is(err, errPoison) {
		t.Fatalf("err = %v, want the transient unknown-purchase-order error", err)
	}
}

func TestNewConsumersWiresFiveTopicsAndNeverDials(t *testing.T) {
	r := newRig()
	groups := Groups{VendorConnectivity: "g1", ShippingAndTracking: "g2", DeliveryPromise: "g3", OfferAvailability: "g4", NodeTopology: "g5"}
	consumers := NewConsumers([]string{"127.0.0.1:1"}, groups, r.intake, nil)
	want := []string{VendorConnectivityTopic, ShippingAndTrackingTopic, DeliveryPromiseTopic, OfferAvailabilityTopic, NodeTopologyTopic}
	if len(consumers) != len(want) {
		t.Fatalf("%d consumers", len(consumers))
	}
	for i, c := range consumers {
		if c.Topic != want[i] || len(c.Handlers) == 0 || c.DLQ == nil || c.Reader == nil {
			t.Errorf("consumer %d = %+v", i, c)
		}
		if err := c.Close(); err != nil {
			t.Errorf("close %s: %v", c.Name, err)
		}
	}
	if got := len(VendorHandlers(r.intake, nil)) + len(ShippingHandlers(r.intake, nil)) + len(DeliveryPromiseHandlers(r.intake, nil)) +
		len(OfferAvailabilityHandlers(r.intake, nil)) + len(NodeTopologyHandlers(r.intake, nil)); got != 13 {
		t.Fatalf("%d handled types, the contract lists 13", got)
	}
	_ = scorecard.MinObservations
}
