package kafka_test

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/application/outbox"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var computedAt = time.Date(2026, 10, 22, 14, 3, 0, 0, time.UTC)

func fixedID() func() string {
	return func() string { return "7d0c6a52-3f7e-4b8e-9a51-2a8f0c1d9e44" }
}

func updated(state scorecard.State, grade scorecard.Grade, m scorecard.Metrics) scorecard.NodeScorecardUpdated {
	return scorecard.NodeScorecardUpdated{
		Header: shared.Header{Version: 7, At: computedAt}, NodeID: "node-0f3a91", Window: scorecard.Window30d,
		State: state, Grade: grade, Metrics: m, ComputedAt: computedAt,
	}
}

func breached() scorecard.NodeThresholdBreached {
	return scorecard.NodeThresholdBreached{
		Header: shared.Header{Version: 8, At: computedAt}, NodeID: "node-0f3a91", Window: scorecard.Window7d,
		Metric: scorecard.MetricOnTimeHandoverRate, Value: 0.88, Threshold: 0.9,
	}
}

var gradedMetrics = scorecard.Metrics{
	OnTimeHandoverRate: 0.962, LateAckRate: 0.021, RejectRate: 0.012, ExpiryRate: 0.004,
	PromiseKeptRate: 0.951, FeedFreshnessP95Minutes: 38, Observations: 1874,
}

// TestGoldenEnvelopes pins the exact bytes of every published event: the
// CloudEvents envelope and the data of apis/asyncapi.yaml, field order and
// number formatting included.
func TestGoldenEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		event  shared.Event
		golden string
	}{
		{"graded scorecard", updated(scorecard.StateGraded, scorecard.GradeB, gradedMetrics), "NodeScorecardUpdated.graded.golden.json"},
		{"scorecard back to PROVISIONAL", updated(scorecard.StateProvisional, "", scorecard.Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 1, Observations: 12}), "NodeScorecardUpdated.provisional.golden.json"},
		{"threshold breached", breached(), "NodeThresholdBreached.golden.json"},
	} {
		msg, err := (&kafka.Encoder{NewID: fixedID()}).Encode(tc.event)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		path := filepath.Join("testdata", tc.golden)
		if *update {
			if err := os.WriteFile(path, append(msg.Value, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := string(msg.Value) + "\n"; got != string(want) {
			t.Errorf("%s: envelope differs from %s\n got %s\nwant %s", tc.name, tc.golden, got, want)
		}
	}
}

func TestEncodeBuildsTheOutboxMessage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		event      shared.Event
		topic      string
		eventType  string
		dataschema string
		subject    string
	}{
		{"integration event", updated(scorecard.StateGraded, scorecard.GradeB, gradedMetrics), kafka.EventsTopic,
			"com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated",
			"urn:retailnetwork:node-performance:events:NodeScorecardUpdated:v1", "node-0f3a91:30d"},
		{"analytics-only event", breached(), kafka.AnalyticsTopic,
			"com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached",
			"urn:retailnetwork:node-performance:analytics:NodeThresholdBreached:v1", "node-0f3a91:7d"},
	} {
		msg, err := (&kafka.Encoder{NewID: fixedID()}).Encode(tc.event)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if msg.Topic != tc.topic || msg.EventType != tc.eventType || msg.DataSchema != tc.dataschema ||
			msg.Subject != tc.subject || string(msg.Key) != tc.subject || msg.EventID != "7d0c6a52-3f7e-4b8e-9a51-2a8f0c1d9e44" {
			t.Errorf("%s: message = %+v", tc.name, msg)
		}
		assertEnvelopeOf(t, tc.name, msg)
	}
}

func TestPublishedTypesAreCatalogued(t *testing.T) {
	adr, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "adr", "0004-cloudevents-envelope-and-type-catalogue.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []shared.Event{updated(scorecard.StateGraded, scorecard.GradeA, gradedMetrics), breached()} {
		msg, err := kafka.NewEncoder().Encode(ev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(adr), "type: "+msg.EventType+" stream: "+streamOf(msg)) {
			t.Errorf("ADR 0004 does not catalogue %s on stream %s", msg.EventType, streamOf(msg))
		}
	}
}

func streamOf(m outbox.Message) string {
	if m.Topic == kafka.AnalyticsTopic {
		return "analytics"
	}
	return "events"
}

func TestEncodeMintsAFreshUUIDPerEvent(t *testing.T) {
	enc := kafka.NewEncoder()
	a, errA := enc.Encode(breached())
	b, errB := enc.Encode(breached())
	if errA != nil || errB != nil || a.EventID == b.EventID || len(a.EventID) != 36 {
		t.Fatalf("ids = %q, %q (%v, %v)", a.EventID, b.EventID, errA, errB)
	}
	if msg, err := (&kafka.Encoder{}).Encode(breached()); err != nil || len(msg.EventID) != 36 {
		t.Fatalf("a nil NewID falls back to uuid: %q, %v", msg.EventID, err)
	}
}

func TestPayloadsCarryTheModelFieldsAndNothingElse(t *testing.T) {
	data := func(ev shared.Event) map[string]any {
		msg, err := kafka.NewEncoder().Encode(ev)
		if err != nil {
			t.Fatal(err)
		}
		var env struct{ Data map[string]any }
		if err := json.Unmarshal(msg.Value, &env); err != nil {
			t.Fatal(err)
		}
		return env.Data
	}
	got := data(updated(scorecard.StateGraded, scorecard.GradeB, gradedMetrics))
	for _, field := range []string{"nodeId", "window", "grade", "onTimeHandoverRate", "lateAckRate", "rejectRate", "expiryRate", "promiseKeptRate", "feedFreshnessP95Minutes", "observations", "computedAt"} {
		if _, ok := got[field]; !ok {
			t.Errorf("NodeScorecardUpdated lacks %s", field)
		}
	}
	if len(got) != 11 {
		t.Errorf("NodeScorecardUpdated has %d fields, the model lists 11: %v", len(got), got)
	}
	got = data(breached())
	if len(got) != 5 || got["metric"] != "onTimeHandoverRate" || got["window"] != "7d" {
		t.Errorf("NodeThresholdBreached = %v", got)
	}
}

type unpublished struct{ shared.Header }

func (unpublished) EventName() string   { return "NotMine" }
func (unpublished) Entity() string      { return "scorecard" }
func (unpublished) SubjectID() string   { return "x" }
func (unpublished) AnalyticsOnly() bool { return false }

func TestEncodeRefusesAnEventItDoesNotPublish(t *testing.T) {
	_, err := kafka.NewEncoder().Encode(unpublished{})
	if err == nil || !strings.Contains(err.Error(), "NotMine") {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, cloudevents.ErrNotCloudEvent) {
		t.Fatal("a programming error is not a bad message")
	}
}

func assertEnvelopeOf(t *testing.T, name string, msg outbox.Message) {
	t.Helper()
	if len(msg.Headers) != 1 || msg.Headers[0] != (outbox.Header{Key: "content-type", Value: cloudevents.MediaType}) {
		t.Errorf("%s: headers = %+v", name, msg.Headers)
	}
	ev, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("%s: the value must be a valid CloudEvent: %v", name, err)
	}
	if ev.ID() != msg.EventID || ev.Type() != msg.EventType || ev.Subject() != msg.Subject || ev.DataSchema() != msg.DataSchema ||
		ev.Source() != "/retailnetwork/node-performance" || !ev.Time().Equal(computedAt) || ev.DataContentType() != "application/json" {
		t.Errorf("%s: envelope = %v", name, ev)
	}
}
