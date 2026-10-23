//go:build integration

package main

// The end-to-end proof, through the real entry point on real Postgres and real
// Kafka: fifty PromiseKept events consumed from the delivery-promise topic
// become a GRADED scorecard that the REST API serves and whose
// NodeScorecardUpdated reaches the integration topic as a CloudEvent.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	inboundkafka "github.com/claudioed/node-performance/internal/adapters/inbound/kafka"
	"github.com/claudioed/node-performance/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/testing/kafkatest"
	"github.com/claudioed/node-performance/internal/testing/pgtest"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// boot starts run() on a fresh database and returns its base URL and a stop
// function that cancels it and waits for run to return.
func boot(t *testing.T) (string, func()) {
	t.Helper()
	addr := freeAddr(t)
	brokers := ""
	for i, b := range kafkatest.Brokers(t) {
		if i > 0 {
			brokers += ","
		}
		brokers += b
	}
	prefix := fmt.Sprintf("itest-%d", time.Now().UnixNano())
	for k, v := range map[string]string{
		"DATABASE_URL": pgtest.FreshURL(t), "KAFKA_BROKERS": brokers, "HTTP_ADDR": addr,
		envVendorGroup: prefix + "-vendor", envShippingGroup: prefix + "-shipping", envPromiseGroup: prefix + "-promise",
		envOfferGroup: prefix + "-offer", envTopologyGroup: prefix + "-topology",
		envRecomputeInterval: "500ms", envRelayInterval: "100ms", envShutdownDrainWait: "0", envLogLevel: "warn",
	} {
		t.Setenv(k, v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	base := "http://" + addr
	waitFor(t, 2*time.Minute, "the service to answer /readyz", func() bool { return status(base+"/readyz") == http.StatusOK })
	return base, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run returned %v after cancellation", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("run did not return after cancellation")
		}
	}
}

func waitFor(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func status(url string) int {
	resp, err := http.Get(url) //nolint:gosec,noctx // a loopback URL of the test
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func keptEvent(t *testing.T, id, node string) kafkatest.Message {
	t.Helper()
	e := ce.New()
	e.SetID(id)
	e.SetType(inboundkafka.TypePromiseKept)
	e.SetSource("/retailnetwork/delivery-promise")
	e.SetSubject("promise-" + id)
	e.SetTime(time.Now().UTC())
	if err := e.SetData(ce.ApplicationJSON, map[string]any{"nodeId": node, "deliveredAt": time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return kafkatest.Message{Key: node, Value: b}
}

func TestPromisesOnKafkaBecomeAGradedScorecardOverRESTAndKafka(t *testing.T) {
	base, stop := boot(t)
	defer stop()

	msgs := make([]kafkatest.Message, 0, 50)
	for i := 0; i < 50; i++ {
		msgs = append(msgs, keptEvent(t, fmt.Sprintf("kept-%d", i), "node-e2e"))
	}
	kafkatest.CreateTopic(t, inboundkafka.DeliveryPromiseTopic)
	kafkatest.Produce(t, inboundkafka.DeliveryPromiseTopic, msgs...)

	var body struct {
		NodeID     string `json:"nodeId"`
		Scorecards []struct {
			ScorecardID string `json:"scorecardId"`
			State       string `json:"state"`
			Grade       string `json:"grade"`
			Metrics     struct {
				Observations    int     `json:"observations"`
				PromiseKeptRate float64 `json:"promiseKeptRate"`
			} `json:"metrics"`
		} `json:"scorecards"`
	}
	waitFor(t, 3*time.Minute, "a GRADED scorecard on the API", func() bool {
		resp, err := http.Get(base + "/scorecards/node-e2e") //nolint:gosec,noctx // a loopback URL of the test
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
			return false
		}
		return len(body.Scorecards) == 2 && body.Scorecards[0].State == "GRADED" && body.Scorecards[1].State == "GRADED"
	})
	for _, s := range body.Scorecards {
		if s.Grade != "A" || s.Metrics.Observations != 50 || s.Metrics.PromiseKeptRate != 1 {
			t.Errorf("scorecard = %+v", s)
		}
	}

	assertPublished(t)

	if status(base+"/scorecards?grade=A") != http.StatusOK || status(base+"/scorecards/nobody") != http.StatusNotFound {
		t.Fatal("the list answers 200 and an unknown node 404")
	}
}

// Kept last on purpose: it stops the shared broker for the rest of the binary.
func TestReadinessGoesAwayWhenKafkaIsGone(t *testing.T) {
	base, stop := boot(t)
	defer stop()
	kafkatest.Stop(t)
	waitFor(t, 60*time.Second, "/readyz to turn 503 once Kafka is gone", func() bool {
		return status(base+"/readyz") == http.StatusServiceUnavailable
	})
	if status(base+"/healthz") != http.StatusOK {
		t.Fatal("liveness does not depend on Kafka")
	}
}

// assertPublished checks the NodeScorecardUpdated of both windows on the
// integration topic: a CloudEvent each, keyed by its subject.
func assertPublished(t *testing.T) {
	t.Helper()
	subjects := map[string]bool{}
	for _, m := range kafkatest.ReadN(t, outboundkafka.EventsTopic, 2, 60*time.Second) {
		ev, err := cloudevents.Decode(m.Value)
		if err != nil || ev.Type() != "com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated" {
			t.Fatalf("published %v, %v", ev, err)
		}
		subjects[ev.Subject()] = true
		if m.Key != ev.Subject() {
			t.Errorf("key %q, subject %q", m.Key, ev.Subject())
		}
	}
	if !subjects["node-e2e:7d"] || !subjects["node-e2e:30d"] {
		t.Fatalf("published subjects = %v", subjects)
	}
}
