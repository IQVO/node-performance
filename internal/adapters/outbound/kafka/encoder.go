// Package kafka is the outbound Kafka adapter: it encodes NodeScorecard domain
// events into CloudEvents 1.0 outbox messages (Encoder), writes drained outbox
// rows to the broker (RelaySink) and answers the readiness question "does a
// broker answer" (BrokerCheck). Envelopes are built ONLY through
// internal/adapters/kafka/cloudevents. Payloads are exactly the ones pinned in
// apis/asyncapi.yaml.
package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/node-performance/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/node-performance/internal/application/outbox"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// The topics of this service. NodeScorecardUpdated goes ONLY to the
// integration topic and NodeThresholdBreached ONLY to the analytics topic.
const (
	EventsTopic    = "retailnetwork.node-performance.events"
	AnalyticsTopic = "retailnetwork.node-performance.analytics"
)

// schemaVersion is the dataschema version of every payload below
// (urn:retailnetwork:node-performance:<stream>:<EventName>:v1).
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON). Timestamps are RFC 3339
// UTC; every field is always present.

type scorecardUpdatedData struct {
	NodeID                  string  `json:"nodeId"`
	Window                  string  `json:"window"`
	Grade                   string  `json:"grade"`
	OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
	LateAckRate             float64 `json:"lateAckRate"`
	RejectRate              float64 `json:"rejectRate"`
	ExpiryRate              float64 `json:"expiryRate"`
	PromiseKeptRate         float64 `json:"promiseKeptRate"`
	FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
	Observations            int     `json:"observations"`
	ComputedAt              string  `json:"computedAt"`
}

type thresholdBreachedData struct {
	NodeID    string  `json:"nodeId"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Window    string  `json:"window"`
}

// Encoder implements outbox.Encoder: each domain event becomes one
// outbox.Message holding the CloudEvents bytes, the Kafka key (the scorecardId)
// and the content-type header. The CloudEvents `id` is minted HERE, once, and
// persisted with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ outbox.Encoder = (*Encoder)(nil)

// Encode implements outbox.Encoder. An event type it does not publish is a
// programming error.
func (e *Encoder) Encode(ev shared.Event) (outbox.Message, error) {
	data, err := payloadFor(ev)
	if err != nil {
		return outbox.Message{}, err
	}
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	stream, topic := cloudevents.StreamEvents, EventsTopic
	if ev.AnalyticsOnly() {
		stream, topic = cloudevents.StreamAnalytics, AnalyticsTopic
	}
	id := newID()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(),
		Time: ev.OccurredAt(), Stream: stream, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: topic, EventType: cloudevents.Type(ev.Entity(), ev.EventName()),
		Subject: ev.SubjectID(), Key: []byte(ev.SubjectID()),
		DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion), Value: value,
		Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case scorecard.NodeScorecardUpdated:
		return scorecardUpdatedData{
			NodeID: string(e.NodeID), Window: string(e.Window), Grade: e.GradeLabel(),
			OnTimeHandoverRate: e.Metrics.OnTimeHandoverRate, LateAckRate: e.Metrics.LateAckRate,
			RejectRate: e.Metrics.RejectRate, ExpiryRate: e.Metrics.ExpiryRate, PromiseKeptRate: e.Metrics.PromiseKeptRate,
			FeedFreshnessP95Minutes: e.Metrics.FeedFreshnessP95Minutes, Observations: e.Metrics.Observations,
			ComputedAt: ts(e.ComputedAt),
		}, nil
	case scorecard.NodeThresholdBreached:
		return thresholdBreachedData{
			NodeID: string(e.NodeID), Metric: string(e.Metric), Value: e.Value, Threshold: e.Threshold, Window: string(e.Window),
		}, nil
	default:
		return nil, fmt.Errorf("kafka encoder: %s is not a published event", ev.EventName())
	}
}
