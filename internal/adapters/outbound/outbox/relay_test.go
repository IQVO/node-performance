package outbox_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	relay "github.com/claudioed/node-performance/internal/adapters/outbound/outbox"
	"github.com/claudioed/node-performance/internal/application/outbox"
)

// fakeStore is a Store over an in-memory queue with the claim semantics of the
// Postgres one: id order, cap filter, stop at the first failure.
type fakeStore struct {
	mu   sync.Mutex
	rows []*row
}

type row struct {
	id        int64
	msg       outbox.Message
	published bool
	attempts  int
}

func newStore(topics ...string) *fakeStore {
	s := &fakeStore{}
	for i, topic := range topics {
		s.rows = append(s.rows, &row{id: int64(i + 1), msg: outbox.Message{EventID: topic, Topic: topic, EventType: "t", Key: []byte("k")}})
	}
	return s
}

func (s *fakeStore) Drain(ctx context.Context, limit, maxAttempts int, send func(context.Context, outbox.Message) error) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	published := 0
	for _, r := range s.rows {
		if r.published || (maxAttempts > 0 && r.attempts >= maxAttempts) {
			continue
		}
		if published >= limit {
			break
		}
		if err := send(ctx, r.msg); err != nil {
			r.attempts++
			return published, &outbox.SendFailure{RowID: r.id, Message: r.msg, Attempts: r.attempts, Err: err}
		}
		r.published, r.attempts = true, r.attempts+1
		published++
	}
	return published, nil
}

type recordingSink struct {
	mu   sync.Mutex
	got  []string
	fail map[string]error
}

func (s *recordingSink) Send(_ context.Context, msgs ...outbox.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		if err := s.fail[m.EventID]; err != nil {
			return err
		}
		s.got = append(s.got, m.EventID)
	}
	return nil
}

func (s *recordingSink) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

func TestRelayOnceSendsInOrderAndStopsAtTheBatchSize(t *testing.T) {
	store, sink := newStore("a", "b", "c"), &recordingSink{}
	r := relay.NewOutboxRelay(store, sink, quiet(), relay.WithBatchSize(2), relay.WithMaxAttempts(3))
	n, err := r.RelayOnce(context.Background())
	if err != nil || n != 2 || strings.Join(sink.sent(), ",") != "a,b" {
		t.Fatalf("first pass: %d, %v, %v", n, err, sink.sent())
	}
	n, err = r.RelayOnce(context.Background())
	if err != nil || n != 1 || strings.Join(sink.sent(), ",") != "a,b,c" {
		t.Fatalf("second pass: %d, %v, %v", n, err, sink.sent())
	}
}

func TestAFailedRowStopsThePassAndKeepsTheOrder(t *testing.T) {
	store := newStore("a", "b", "c")
	sink := &recordingSink{fail: map[string]error{"b": errors.New("broker down")}}
	r := relay.NewOutboxRelay(store, sink, quiet(), relay.WithMaxAttempts(5))
	n, err := r.RelayOnce(context.Background())
	var sf *outbox.SendFailure
	if !errors.As(err, &sf) || sf.RowID != 2 || sf.Attempts != 1 || n != 1 {
		t.Fatalf("pass = %d, %v", n, err)
	}
	if got := strings.Join(sink.sent(), ","); got != "a" {
		t.Fatalf("a later row must not overtake a failed one: %q", got)
	}
}

func TestTheAttemptCapExcludesARowAndLogsItOnce(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	store := newStore("a", "b")
	sink := &recordingSink{fail: map[string]error{"a": errors.New("poison")}}
	r := relay.NewOutboxRelay(store, sink, logger, relay.WithMaxAttempts(2))
	for pass := 1; pass <= 2; pass++ {
		if _, err := r.RelayOnce(context.Background()); err == nil {
			t.Fatalf("pass %d must fail on the poison row", pass)
		}
	}
	if strings.Count(logs.String(), relay.AttemptCapReachedMsg) != 1 {
		t.Fatalf("the cap must be logged once, at the failure that reaches it:\n%s", logs.String())
	}
	n, err := r.RelayOnce(context.Background())
	if err != nil || n != 1 || strings.Join(sink.sent(), ",") != "b" {
		t.Fatalf("after the cap the next row overtakes: %d, %v, %v", n, err, sink.sent())
	}
}

func TestWithoutACapNothingIsLoggedAsCapped(t *testing.T) {
	var logs bytes.Buffer
	store := newStore("a")
	sink := &recordingSink{fail: map[string]error{"a": errors.New("poison")}}
	r := relay.NewOutboxRelay(store, sink, slog.New(slog.NewTextHandler(&logs, nil)))
	for i := 0; i < 3; i++ {
		_, _ = r.RelayOnce(context.Background())
	}
	if strings.Contains(logs.String(), relay.AttemptCapReachedMsg) {
		t.Fatal("an unlimited relay never reaches a cap")
	}
}

func TestOptionsIgnoreNonsense(t *testing.T) {
	store, sink := newStore("a", "b"), &recordingSink{fail: map[string]error{"a": errors.New("x")}}
	r := relay.NewOutboxRelay(store, sink, nil, relay.WithBatchSize(0), relay.WithInterval(-1), relay.WithMaxAttempts(-4))
	if n, err := r.RelayOnce(context.Background()); err == nil || n != 0 {
		t.Fatalf("defaults stay (batch 100, no cap): %d, %v", n, err)
	}
	if relay.DefaultMaxAttempts != 20 || relay.DefaultBatchSize != 100 || relay.DefaultInterval != time.Second {
		t.Fatal("the documented defaults changed")
	}
}

func TestRunDrainsUntilTheContextIsDone(t *testing.T) {
	store, sink := newStore("a", "b", "c"), &recordingSink{}
	r := relay.NewOutboxRelay(store, sink, quiet(), relay.WithBatchSize(2), relay.WithInterval(5*time.Millisecond), relay.WithMaxAttempts(3))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	deadline := time.After(5 * time.Second)
	for len(sink.sent()) < 3 {
		select {
		case <-deadline:
			t.Fatalf("relay sent only %v", sink.sent())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a Run on a cancelled context returns at once: %v", err)
	}
}

func TestRunSurvivesAFailingPass(t *testing.T) {
	store := newStore("a")
	sink := &recordingSink{fail: map[string]error{"a": errors.New("down")}}
	r := relay.NewOutboxRelay(store, sink, quiet(), relay.WithInterval(5*time.Millisecond), relay.WithMaxAttempts(1000))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v", err)
	}
	store.mu.Lock()
	attempts := store.rows[0].attempts
	store.mu.Unlock()
	if attempts < 2 {
		t.Fatalf("a failing pass is retried after the interval, attempts = %d", attempts)
	}
}
