package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/application/usecases"
)

type ceEvent = ce.Event

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// rig is the real intake use cases over the in-memory store.
type rig struct {
	store  *memory.Store
	intake usecases.Intake
	logs   *bytes.Buffer
}

func newRig() *rig {
	s := memory.NewStore()
	d := usecases.Deps{Scorecards: s.Scorecards(), Facts: s.Facts(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: memory.NewClock(t0)}
	return &rig{store: s, intake: usecases.NewIntake(d), logs: &bytes.Buffer{}}
}

func (r *rig) consumer(topic string, handlers func(usecases.Intake, *slog.Logger) map[string]HandlerFunc) *Consumer {
	logger := slog.New(slog.NewTextHandler(r.logs, nil))
	return &Consumer{Name: "test consumer", Topic: topic, Handlers: handlers(r.intake, logger), Logger: logger}
}

// event builds the bytes of a structured-mode CloudEvent of the given type.
func event(t testing.TB, id, typ string, data any) []byte {
	t.Helper()
	e := ce.New()
	e.SetID(id)
	e.SetType(typ)
	e.SetSource("/retailnetwork/producer")
	e.SetSubject("subject")
	e.SetTime(t0)
	if raw, ok := data.([]byte); ok {
		_ = e.SetData(ce.ApplicationJSON, json.RawMessage(raw))
	} else if err := e.SetData(ce.ApplicationJSON, data); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func at(minutes int) string { return t0.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339) }

// fakeReader serves fetchErrs then msgs in order, records commits, then
// fails with alwaysErr, or with end once alwaysErr is exhausted.
type fakeReader struct {
	mu        sync.Mutex
	msgs      []kafkago.Message
	committed []kafkago.Message
	commitErr []error // consumed one per CommitMessages call
	fetchErrs []error // consumed one per FetchMessage call, BEFORE msgs
	alwaysErr error   // returned once msgs and fetchErrs are exhausted
	end       error
	closed    bool
}

func (f *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	f.mu.Lock()
	if len(f.fetchErrs) > 0 {
		err := f.fetchErrs[0]
		f.fetchErrs = f.fetchErrs[1:]
		f.mu.Unlock()
		return kafkago.Message{}, err
	}
	if len(f.msgs) > 0 {
		m := f.msgs[0]
		f.msgs = f.msgs[1:]
		f.mu.Unlock()
		return m, nil
	}
	always, end := f.alwaysErr, f.end
	f.mu.Unlock()
	if always != nil {
		return kafkago.Message{}, always
	}
	return kafkago.Message{}, end
}

func (f *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commitErr) > 0 {
		err := f.commitErr[0]
		f.commitErr = f.commitErr[1:]
		if err != nil {
			return err
		}
	}
	f.committed = append(f.committed, msgs...)
	return nil
}

func (f *fakeReader) Close() error { f.closed = true; return nil }

// fakeDLQ records dead letters and fails the first len(errs) writes.
type fakeDLQ struct {
	mu     sync.Mutex
	got    []kafkago.Message
	errs   []error
	closed bool
}

func (f *fakeDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return err
		}
	}
	f.got = append(f.got, msgs...)
	return nil
}

func (f *fakeDLQ) Close() error { f.closed = true; return nil }

// noSleep records the backoff sequence instead of sleeping.
type noSleep struct{ delays []time.Duration }

func (n *noSleep) sleep(_ context.Context, d time.Duration) error {
	n.delays = append(n.delays, d)
	return nil
}
