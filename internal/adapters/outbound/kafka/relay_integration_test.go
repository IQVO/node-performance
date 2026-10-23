//go:build integration

package kafka_test

// The relay proof: scorecard events written through the transactional outbox
// of a REAL Postgres reach a REAL Kafka broker as the exact CloudEvents the
// encoder built, on the right topic, with the right key and header.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	segmentio "github.com/segmentio/kafka-go"
	_ "github.com/testcontainers/testcontainers-go/modules/kafka" // the broker comes from internal/testing/kafkatest

	"github.com/claudioed/node-performance/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/adapters/outbound/outbox"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
	"github.com/claudioed/node-performance/internal/testing/kafkatest"
	"github.com/claudioed/node-performance/internal/testing/pgtest"
)

func TestTheRelayPublishesTheOutboxToKafkaOnTheRightTopics(t *testing.T) {
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, pgtest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	kafkatest.CreateTopic(t, kafka.EventsTopic)
	kafkatest.CreateTopic(t, kafka.AnalyticsTopic)

	events := repocontract.RaisedEvents(t) // Updated, Updated, Breached
	repo := postgres.NewOutboxRepo(pool, kafka.NewEncoder())
	if err := repo.Append(ctx, events...); err != nil {
		t.Fatal(err)
	}

	sink := kafka.NewRelaySink(joinBrokers(kafkatest.Brokers(t)))
	t.Cleanup(func() { _ = sink.Close() })
	relay := outbox.NewOutboxRelay(postgres.NewRelayStore(pool), sink, slog.Default(), outbox.WithMaxAttempts(5))
	n, err := relay.RelayOnce(ctx)
	if err != nil || n != 3 {
		t.Fatalf("RelayOnce = %d, %v", n, err)
	}

	onEvents := kafkatest.ReadN(t, kafka.EventsTopic, 2, 30*time.Second)
	onAnalytics := kafkatest.ReadN(t, kafka.AnalyticsTopic, 1, 30*time.Second)
	for _, m := range onEvents {
		assertEnvelope(t, m, "com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated", "node-9:7d")
	}
	assertEnvelope(t, onAnalytics[0], "com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached", "node-9:7d")
	if extra := kafkatest.CountAvailable(t, kafka.EventsTopic); extra != 2 {
		t.Fatalf("the analytics-only event must not reach the integration topic: %d messages", extra)
	}

	var unpublished int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&unpublished); err != nil || unpublished != 0 {
		t.Fatalf("every row is marked published: %d left, %v", unpublished, err)
	}
	again, err := relay.RelayOnce(ctx)
	if err != nil || again != 0 {
		t.Fatalf("a second pass has nothing to send: %d, %v", again, err)
	}
}

func assertEnvelope(t *testing.T, m kafkatest.Message, wantType, wantKey string) {
	t.Helper()
	if m.Key != wantKey || m.Headers["content-type"] != cloudevents.MediaType {
		t.Fatalf("key %q headers %v", m.Key, m.Headers)
	}
	ev, err := cloudevents.Decode(m.Value)
	if err != nil {
		t.Fatalf("not a CloudEvent: %v", err)
	}
	if ev.Type() != wantType || ev.Subject() != wantKey || ev.Source() != "/retailnetwork/node-performance" {
		t.Fatalf("envelope = %v", ev)
	}
}

func joinBrokers(brokers []string) string {
	out := ""
	for i, b := range brokers {
		if i > 0 {
			out += ","
		}
		out += b
	}
	return out
}

// the relay writes only to the topics the messages name: a sink built on an
// unreachable broker fails the pass and leaves the row unpublished.
func TestAnUnreachableBrokerLeavesTheRowUnpublished(t *testing.T) {
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, pgtest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.NewOutboxRepo(pool, kafka.NewEncoder()).Append(ctx, repocontract.RaisedEvents(t)[0]); err != nil {
		t.Fatal(err)
	}
	dead := kafka.NewRelaySinkWithWriter(&segmentio.Writer{Addr: segmentio.TCP("127.0.0.1:1"), RequiredAcks: segmentio.RequireAll, WriteTimeout: time.Second, MaxAttempts: 1})
	relay := outbox.NewOutboxRelay(postgres.NewRelayStore(pool), dead, slog.Default(), outbox.WithMaxAttempts(5))
	if n, err := relay.RelayOnce(ctx); err == nil || n != 0 {
		t.Fatalf("RelayOnce = %d, %v; want a failure", n, err)
	}
	var attempts int
	var lastError *string
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error FROM outbox_events`).Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastError == nil {
		t.Fatalf("the failure is recorded on the row: attempts %d error %v", attempts, lastError)
	}
}
