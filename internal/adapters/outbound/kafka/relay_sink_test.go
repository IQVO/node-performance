package kafka

import (
	"context"
	"errors"
	"testing"

	segmentio "github.com/segmentio/kafka-go"

	"github.com/claudioed/node-performance/internal/application/outbox"
)

type fakeWriter struct {
	got []segmentio.Message
	err error
}

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...segmentio.Message) error {
	f.got = append(f.got, msgs...)
	return f.err
}

func TestRelaySinkWritesToTheTopicEachMessageNames(t *testing.T) {
	w := &fakeWriter{}
	sink := NewRelaySinkWithWriter(w)
	err := sink.Send(context.Background(),
		outbox.Message{Topic: EventsTopic, Key: []byte("n:30d"), Value: []byte("a"), Headers: []outbox.Header{{Key: "content-type", Value: "ct"}}},
		outbox.Message{Topic: AnalyticsTopic, Key: []byte("n:7d"), Value: []byte("b")})
	if err != nil || len(w.got) != 2 {
		t.Fatalf("Send = %v, %d messages", err, len(w.got))
	}
	first := w.got[0]
	if first.Topic != EventsTopic || string(first.Key) != "n:30d" || string(first.Value) != "a" ||
		len(first.Headers) != 1 || first.Headers[0].Key != "content-type" || string(first.Headers[0].Value) != "ct" {
		t.Fatalf("first message = %+v", first)
	}
	if w.got[1].Topic != AnalyticsTopic || len(w.got[1].Headers) != 0 {
		t.Fatalf("second message = %+v", w.got[1])
	}
}

func TestRelaySinkRefusesAMessageWithoutATopicAndWrapsWriteErrors(t *testing.T) {
	w := &fakeWriter{}
	if err := NewRelaySinkWithWriter(w).Send(context.Background(), outbox.Message{EventType: "t"}); err == nil || len(w.got) != 0 {
		t.Fatalf("a message without a topic must be refused before writing: %v", err)
	}
	boom := errors.New("broker down")
	if err := NewRelaySinkWithWriter(&fakeWriter{err: boom}).Send(context.Background(), outbox.Message{Topic: EventsTopic}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if err := NewRelaySinkWithWriter(w).Send(context.Background()); err != nil {
		t.Fatalf("sending nothing is a no-op: %v", err)
	}
}

func TestNewRelaySinkNeverDialsAndCloses(t *testing.T) {
	sink := NewRelaySink("localhost:1, localhost:2")
	w, ok := sink.writer.(*segmentio.Writer)
	if !ok || w.Topic != "" || w.RequiredAcks != segmentio.RequireAll || w.BatchTimeout != syncWriterBatchTimeout || !w.AllowAutoTopicCreation {
		t.Fatalf("writer = %+v", sink.writer)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := NewRelaySinkWithWriter(&fakeWriter{}).Close(); err != nil {
		t.Fatalf("closing a fake writer is a no-op: %v", err)
	}
}
