package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

func (e env) shipped(t *testing.T, id, shipment, node string, minutes int) usecases.Outcome {
	t.Helper()
	out, err := e.intake.InboundShipmentShipped(context.Background(), usecases.InboundShipmentShipped{
		Ref: ref(id, 0), ShipmentID: shipment, NodeID: node, ShippedAt: at(minutes),
	})
	if err != nil {
		t.Fatalf("shipped %s: %v", shipment, err)
	}
	return out
}

func (e env) received(t *testing.T, id, shipment, node string, minutes int, statuses ...string) usecases.Outcome {
	t.Helper()
	out, err := e.intake.InboundReceiptSubmitted(context.Background(), usecases.InboundReceiptSubmitted{
		Ref: ref(id, 0), ShipmentID: shipment, NodeID: node, ReceivedAt: at(minutes), CartonStatuses: statuses,
	})
	if err != nil {
		t.Fatalf("received %s: %v", shipment, err)
	}
	return out
}

func (e env) inboundOf(t *testing.T, node string, window scorecard.Window) (*int, *float64) {
	t.Helper()
	// a scorecard is needed for the read: one feed creates both windows.
	_, _ = e.intake.InventoryFeedApplied(context.Background(), usecases.InventoryFeedApplied{Ref: ref("feed-"+node, 0), NodeID: node, AppliedAt: at(0)})
	got, err := e.queries.GetScorecard(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Scorecards {
		if s.Window == string(window) {
			return s.Metrics.InboundTimelinessP95Minutes, s.Metrics.InboundAccuracyRate
		}
	}
	t.Fatalf("no %s scorecard", window)
	return nil, nil
}

func TestInboundReceiptAndShipmentJoinInEitherOrder(t *testing.T) {
	for name, order := range map[string]bool{"shipment first": true, "receipt first": false} {
		t.Run(name, func(t *testing.T) {
			e := newEnv()
			e.clock.Set(at(600))
			if order {
				e.shipped(t, "s1", "shp-1", "node-1", 0)
				e.received(t, "r1", "shp-1", "node-1", 90, "RECEIVED", "DAMAGED")
			} else {
				e.received(t, "r1", "shp-1", "node-1", 90, "RECEIVED", "DAMAGED")
				if p95, acc := e.inboundOf(t, "node-1", scorecard.Window7d); p95 != nil || acc != nil {
					t.Fatalf("a parked receipt must not show: %v %v", p95, acc)
				}
				e.shipped(t, "s1", "shp-1", "node-1", 0)
			}
			p95, acc := e.inboundOf(t, "node-1", scorecard.Window7d)
			if p95 == nil || *p95 != 90 || acc == nil || *acc != 0.5 {
				t.Fatalf("p95 %v accuracy %v", p95, acc)
			}
		})
	}
}

func TestInboundEventsAreIdempotentAndCountedOnce(t *testing.T) {
	e := newEnv()
	e.clock.Set(at(600))
	if out := e.shipped(t, "s1", "shp-1", "node-1", 0); out != usecases.OutcomeApplied {
		t.Fatalf("first shipment: %s", out)
	}
	if out := e.shipped(t, "s1", "shp-1", "node-1", 0); out != usecases.OutcomeDuplicate {
		t.Fatalf("same event id: %s", out)
	}
	if out := e.shipped(t, "s2", "shp-1", "node-1", 300); out != usecases.OutcomeDuplicate {
		t.Fatalf("same shipment, new event id, keeps the first instant: %s", out)
	}
	if out := e.received(t, "r1", "shp-1", "node-1", 90, "RECEIVED"); out != usecases.OutcomeApplied {
		t.Fatalf("first receipt: %s", out)
	}
	if out := e.received(t, "r1", "shp-1", "node-1", 90, "RECEIVED"); out != usecases.OutcomeDuplicate {
		t.Fatalf("same receipt event id: %s", out)
	}
	if out := e.received(t, "r2", "shp-1", "node-1", 200, "DAMAGED"); out != usecases.OutcomeDuplicate {
		t.Fatalf("second receipt of the shipment: %s", out)
	}
	p95, acc := e.inboundOf(t, "node-1", scorecard.Window30d)
	if *p95 != 90 || *acc != 1 {
		t.Fatalf("the first shipped instant and the first receipt must stand: %d %v", *p95, *acc)
	}
	if got := e.store.Counts(); got.Shipments != 1 || got.Receipts != 1 {
		t.Fatalf("counts %+v", got)
	}
}

func TestInboundEventsNeverTouchScorecardsOrTheOutbox(t *testing.T) {
	e := newEnv()
	e.shipped(t, "s1", "shp-1", "node-1", 0)
	e.received(t, "r1", "shp-1", "node-1", 90, "RECEIVED")
	if got := e.store.Counts(); got.Scorecards != 0 || got.OutboxEvents != 0 || got.Processed != 2 {
		t.Fatalf("counts %+v", got)
	}
}

func TestInboundInvalidEventsAreInvalidEvents(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	cases := map[string]func() error{
		"shipment without id": func() error {
			_, err := e.intake.InboundShipmentShipped(ctx, usecases.InboundShipmentShipped{Ref: ref("a", 0), NodeID: "n", ShippedAt: at(0)})
			return err
		},
		"shipment without instant": func() error {
			_, err := e.intake.InboundShipmentShipped(ctx, usecases.InboundShipmentShipped{Ref: ref("b", 0), ShipmentID: "s", NodeID: "n"})
			return err
		},
		"receipt without node": func() error {
			_, err := e.intake.InboundReceiptSubmitted(ctx, usecases.InboundReceiptSubmitted{Ref: ref("c", 0), ShipmentID: "s", ReceivedAt: at(0)})
			return err
		},
		"receipt with a colon in the node": func() error {
			_, err := e.intake.InboundReceiptSubmitted(ctx, usecases.InboundReceiptSubmitted{Ref: ref("d", 0), ShipmentID: "s", NodeID: "a:b", ReceivedAt: at(0)})
			return err
		},
		"receipt without instant": func() error {
			_, err := e.intake.InboundReceiptSubmitted(ctx, usecases.InboundReceiptSubmitted{Ref: ref("e", 0), ShipmentID: "s", NodeID: "n"})
			return err
		},
	}
	for name, f := range cases {
		if err := f(); !errors.Is(err, usecases.ErrInvalidEvent) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := e.store.Counts(); got.Processed != 0 || got.Shipments != 0 || got.Receipts != 0 {
		t.Fatalf("an invalid event leaves nothing behind (inbox row rolled back): %+v", got)
	}
}

func TestInboundMetricsFollowEachWindow(t *testing.T) {
	e := newEnv()
	day := 24 * 60
	e.clock.Set(at(30 * day))
	e.shipped(t, "s1", "shp-1", "node-1", 30*day-10*day-100)
	e.received(t, "r1", "shp-1", "node-1", 30*day-10*day, "RECEIVED")
	e.shipped(t, "s2", "shp-2", "node-1", 30*day-50)
	e.received(t, "r2", "shp-2", "node-1", 30*day-5, "DAMAGED")
	p7, a7 := e.inboundOf(t, "node-1", scorecard.Window7d)
	if *p7 != 45 || *a7 != 0 {
		t.Fatalf("7d: %d %v", *p7, *a7)
	}
	p30, a30 := e.inboundOf(t, "node-1", scorecard.Window30d)
	if *p30 != 100 || *a30 != 0.5 {
		t.Fatalf("30d: %d %v", *p30, *a30)
	}
}

func TestInboundReadFailureIsReturned(t *testing.T) {
	e := newEnv()
	e.clock.Set(at(10))
	_, _ = e.inboundOf(t, "node-1", scorecard.Window7d)
	q := usecases.Queries{Reader: e.store.Reader(), Inbound: failingInbound{errBoom}, Clock: e.clock}
	if _, err := q.GetScorecard(context.Background(), "node-1"); !errors.Is(err, errBoom) {
		t.Fatalf("get: %v", err)
	}
	if _, err := q.ListScorecardsByGrade(context.Background(), usecases.ListByGradeInput{Grade: "PROVISIONAL"}); !errors.Is(err, errBoom) {
		t.Fatalf("list: %v", err)
	}
}

func TestListAddsInboundMetricsAndSkipsAnEmptyPage(t *testing.T) {
	e := newEnv()
	e.clock.Set(at(600))
	e.shipped(t, "s1", "shp-1", "node-1", 0)
	e.received(t, "r1", "shp-1", "node-1", 60, "RECEIVED")
	_, _ = e.inboundOf(t, "node-1", scorecard.Window7d)
	page, err := e.queries.ListScorecardsByGrade(context.Background(), usecases.ListByGradeInput{Grade: "PROVISIONAL"})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("page %+v %v", page, err)
	}
	for _, it := range page.Items {
		if it.Metrics.InboundTimelinessP95Minutes == nil || *it.Metrics.InboundTimelinessP95Minutes != 60 {
			t.Fatalf("%s: %v", it.ScorecardID, it.Metrics.InboundTimelinessP95Minutes)
		}
	}
	// an empty page never reaches the inbound reader
	q := usecases.Queries{Reader: e.store.Reader(), Inbound: failingInbound{errBoom}, Clock: e.clock}
	if _, err := q.ListScorecardsByGrade(context.Background(), usecases.ListByGradeInput{Grade: "A"}); err != nil {
		t.Fatalf("empty page: %v", err)
	}
	// without an inbound reader the metrics stay out
	plain := usecases.Queries{Reader: e.store.Reader()}
	got, err := plain.GetScorecard(context.Background(), "node-1")
	if err != nil || got.Scorecards[0].Metrics.InboundTimelinessP95Minutes != nil {
		t.Fatalf("plain read: %+v %v", got, err)
	}
}

type failingInbound struct{ err error }

func (f failingInbound) Observations(context.Context, []scorecard.NodeID, time.Time) (map[scorecard.NodeID][]scorecard.InboundObservation, error) {
	return nil, f.err
}
