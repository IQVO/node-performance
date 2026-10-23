// Package kafkatest boots ONE real Kafka broker through testcontainers per
// test binary. It never reads KAFKA_BROKERS, never hardcodes a broker address
// and never skips: a missing Docker is a test failure (fleet rule,
// TestKafkaIntegrationTestsUseTestcontainers). It is test support: nothing under
// cmd/ imports it.
package kafkatest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
)

var (
	once      sync.Once
	container *tckafka.KafkaContainer
	brokers   []string
	startErr  error
	seq       atomic.Int64
)

// Brokers starts the shared broker on first use and returns its addresses.
func Brokers(t testing.TB) []string {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		container, startErr = tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("node-performance-itest"))
		if startErr != nil {
			return
		}
		brokers, startErr = container.Brokers(ctx)
	})
	if startErr != nil {
		t.Fatalf("start kafka container: %v", startErr)
	}
	return brokers
}

// Stop stops the shared broker (it stays stopped for the rest of the binary):
// the readiness test of cmd/api uses it to take Kafka away from a running
// service.
func Stop(t testing.TB) {
	t.Helper()
	Brokers(t)
	timeout := 30 * time.Second
	if err := container.Stop(context.Background(), &timeout); err != nil {
		t.Fatalf("stop kafka container: %v", err)
	}
}

// Topic creates a unique one-partition topic named "<prefix>.itest-<n>" and
// waits until it has a leader (CreateTopics returns before the broker is
// done), then returns its name.
func Topic(t testing.TB, prefix string) string {
	t.Helper()
	topic := fmt.Sprintf("%s.itest-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
	CreateTopic(t, topic)
	return topic
}

// CreateTopic creates a one-partition topic and waits for its leader.
func CreateTopic(t testing.TB, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", Brokers(t)[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// Message is a consumed or produced Kafka message in neutral terms, so test
// files need not touch the Kafka client themselves.
type Message struct {
	Key     string
	Value   []byte
	Headers map[string]string
}

// ReadN reads exactly n messages from the single partition of topic, from the
// start, and fails the test if they do not arrive within timeout.
func ReadN(t testing.TB, topic string, n int, timeout time.Duration) []Message {
	t.Helper()
	reader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: Brokers(t), Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer func() { _ = reader.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out := make([]Message, 0, n)
	for len(out) < n {
		m, err := reader.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read message %d of %d from %s: %v", len(out)+1, n, topic, err)
		}
		headers := make(map[string]string, len(m.Headers))
		for _, h := range m.Headers {
			headers[h.Key] = string(h.Value)
		}
		out = append(out, Message{Key: string(m.Key), Value: m.Value, Headers: headers})
	}
	return out
}

// CountAvailable reports how many messages topic holds right now (end offset of
// its single partition), without consuming.
func CountAvailable(t testing.TB, topic string) int64 {
	t.Helper()
	conn, err := kafkago.DialLeader(context.Background(), "tcp", Brokers(t)[0], topic, 0)
	if err != nil {
		t.Fatalf("dial leader of %s: %v", topic, err)
	}
	defer func() { _ = conn.Close() }()
	last, err := conn.ReadLastOffset()
	if err != nil {
		t.Fatalf("last offset of %s: %v", topic, err)
	}
	return last
}

// Produce writes messages to topic (key and headers as given) and waits for
// the broker's acknowledgement.
func Produce(t testing.TB, topic string, msgs ...Message) {
	t.Helper()
	w := &kafkago.Writer{Addr: kafkago.TCP(Brokers(t)...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond}
	defer func() { _ = w.Close() }()
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		out[i] = kafkago.Message{Key: []byte(m.Key), Value: m.Value}
		for k, v := range m.Headers {
			out[i].Headers = append(out[i].Headers, kafkago.Header{Key: k, Value: []byte(v)})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A just-created topic can still be unknown to the broker that serves the
	// produce request even once its leader is visible, so retry that one error.
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		if err = w.WriteMessages(ctx, out...); err == nil || !errors.Is(err, kafkago.UnknownTopicOrPartition) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("produce to %s: %v", topic, err)
	}
}

// Shutdown terminates the shared broker; call it from TestMain.
func Shutdown() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}
