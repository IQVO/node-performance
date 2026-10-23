//go:build integration

package kafka

// The consumer proof: events produced to a REAL Kafka topic are applied to a
// REAL Postgres through the intake use cases, exactly once, with offsets
// committed after the effect; a poison message lands on "<topic>.dlq".

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/clock"
	outkafka "github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/testing/kafkatest"
	"github.com/claudioed/node-performance/internal/testing/pgtest"
)

func TestConsumerAppliesKafkaEventsToPostgresOnceAndDeadLettersPoison(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := postgres.NewPool(ctx, pgtest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	deps := usecases.Deps{
		Scorecards: postgres.NewScorecardRepo(pool), Facts: postgres.NewPurchaseOrderFactsRepo(pool),
		Inbox: postgres.NewInboxRepo(pool), Outbox: postgres.NewOutboxRepo(pool, outkafka.NewEncoder()),
		UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}
	intake := usecases.NewIntake(deps)

	topic := kafkatest.Topic(t, "retailnetwork.delivery-promise")
	brokers := kafkatest.Brokers(t)
	c := newConsumer("itest consumer", brokers, topic, "itest-group-"+topic, DeliveryPromiseHandlers(intake, nil), nil)
	c.Retry = RetryPolicy{Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	t.Cleanup(func() { _ = c.Close() })

	good := func(id string) kafkatest.Message {
		return kafkatest.Message{Key: "n1", Value: event(t, id, TypePromiseKept, map[string]any{"nodeId": "n1", "deliveredAt": at(1)})}
	}
	kafkatest.Produce(t, topic, good("k1"), good("k1"), kafkatest.Message{Key: "n1", Value: event(t, "bad", TypePromiseKept, map[string]any{"deliveredAt": at(1)})}, good("k2"))

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.Now().Add(90 * time.Second)
	for {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM scorecard_observations WHERE scorecard_id = 'n1:30d'`).Scan(&n); err == nil && n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the two distinct events were not applied")
		}
		time.Sleep(200 * time.Millisecond)
	}
	dead := kafkatest.ReadN(t, topic+DLQSuffix, 1, 60*time.Second)
	if dead[0].Headers["x-dlq-source-topic"] != topic || dead[0].Headers["x-dlq-error"] == "" {
		t.Fatalf("dead letter headers = %v", dead[0].Headers)
	}
	var processed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&processed); err != nil || processed != 2 {
		t.Fatalf("the duplicate and the poison leave no inbox row: %d, %v", processed, err)
	}
	cancel()
	<-done
}
