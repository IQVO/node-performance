//go:build integration

package kafka

// The inbound proof: a receipt produced to a REAL vendor-connectivity topic
// BEFORE its shipment is parked in a REAL Postgres, joins when the shipment
// arrives on the seller-inbound topic, and a redelivery counts once.

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/clock"
	outkafka "github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/testing/kafkatest"
	"github.com/claudioed/node-performance/internal/testing/pgtest"
)

func TestInboundReceiptParkedThenJoinedByTheShipmentOverRealKafkaAndPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := postgres.NewPool(ctx, pgtest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	inbound := postgres.NewInboundRepo(pool)
	count := func(table string) int {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n)
		return n
	}
	deps := usecases.Deps{
		Scorecards: postgres.NewScorecardRepo(pool), Facts: postgres.NewPurchaseOrderFactsRepo(pool), Inbound: inbound,
		Inbox: postgres.NewInboxRepo(pool), Outbox: postgres.NewOutboxRepo(pool, outkafka.NewEncoder()),
		UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}
	intake := usecases.NewIntake(deps)
	brokers := kafkatest.Brokers(t)

	vendorTopic := kafkatest.Topic(t, "retailnetwork.vendor-connectivity")
	sellerTopic := kafkatest.Topic(t, "retailnetwork.seller-inbound")
	retry := RetryPolicy{Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	vendor := newConsumer("itest vendor consumer", brokers, vendorTopic, "itest-vendor-"+vendorTopic, VendorHandlers(intake, nil), nil)
	seller := newConsumer("itest seller consumer", brokers, sellerTopic, "itest-seller-"+sellerTopic, SellerInboundHandlers(intake, nil), nil)
	for _, c := range []*Consumer{vendor, seller} {
		c.Retry = retry
		t.Cleanup(func() { _ = c.Close() })
	}
	done := make(chan error, 2)
	go func() { done <- vendor.Run(ctx) }()
	go func() { done <- seller.Run(ctx) }()

	// The receipt (twice, one redelivery) goes first; nothing may be counted.
	receipt := kafkatest.Message{Key: "shp-1", Value: event(t, "r1", TypeInboundReceiptSubmitted, receiptData("shp-1", "RECEIVED", "RECEIVED", "RECEIVED", "DAMAGED"))}
	kafkatest.Produce(t, vendorTopic, receipt, receipt)
	waitFor(ctx, t, "the receipt is parked", func() bool { return count("inbound_receipts") == 1 })
	nodes := []scorecard.NodeID{"n1"}
	got, err := inbound.Observations(ctx, nodes, time.Time{})
	if err != nil || len(got["n1"]) != 0 {
		t.Fatalf("a parked receipt must not be counted: %v %v", got, err)
	}

	// The shipment arrives: the receipt joins, counted once.
	shipped := kafkatest.Message{Key: "shp-1", Value: event(t, "s1", TypeInboundShipmentShipped, map[string]any{"shipmentId": "shp-1", "planId": "p", "sellerId": "s", "nodeId": "n1", "shippedAt": at(-30)})}
	kafkatest.Produce(t, sellerTopic, shipped, shipped)
	waitFor(ctx, t, "the shipment is stored", func() bool { return count("inbound_shipments") == 1 })
	got, err = inbound.Observations(ctx, nodes, time.Time{})
	if err != nil || len(got["n1"]) != 1 {
		t.Fatalf("the receipt must join its shipment once: %v %v", got, err)
	}
	m := scorecard.ComputeInbound(got["n1"], t0.Add(time.Hour), scorecard.Window7d)
	if m.TimelinessP95Minutes == nil || *m.TimelinessP95Minutes != 120 || m.AccuracyRate == nil || *m.AccuracyRate != 0.75 {
		t.Fatalf("metrics = %+v", m)
	}
	if n := count("processed_events"); n != 2 {
		t.Fatalf("one inbox row per distinct event id: %d", n)
	}
	cancel()
	<-done
	<-done
}

func waitFor(ctx context.Context, t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for !cond() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
