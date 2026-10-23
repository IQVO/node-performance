package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

var errBoom = errors.New("database down")

// run starts c.Run over reader/dlq with a recording sleeper.
func run(t *testing.T, c *Consumer, reader *fakeReader, dlq *fakeDLQ) (*noSleep, error) {
	t.Helper()
	ns := &noSleep{}
	c.Reader, c.DLQ, c.sleep = reader, dlq, ns.sleep
	return ns, c.Run(context.Background())
}

func okKept(t *testing.T, id string) kafkago.Message {
	return kafkago.Message{Topic: DeliveryPromiseTopic, Key: []byte("k"), Value: event(t, id, TypePromiseKept, map[string]any{"nodeId": "n1", "deliveredAt": at(1)})}
}

func TestMessagesAreCommittedOnlyAfterTheyAreHandled(t *testing.T) {
	r := newRig()
	end := errors.New("reader closed")
	reader := &fakeReader{msgs: []kafkago.Message{okKept(t, "a"), okKept(t, "b")}, end: end}
	_, err := run(t, r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers), reader, &fakeDLQ{})
	if !errors.Is(err, end) || len(reader.committed) != 2 || counted(t, r, "n1:30d") != 2 {
		t.Fatalf("err %v committed %d", err, len(reader.committed))
	}
}

func TestATransientFailureIsRetriedWithBackoffThenSucceeds(t *testing.T) {
	c := &Consumer{Name: "c", Topic: "t", Logger: quiet()}
	calls := 0
	c.Handlers = map[string]HandlerFunc{TypePromiseKept: func(context.Context, ceEvent) error {
		calls++
		if calls < 3 {
			return errBoom
		}
		return nil
	}}
	reader := &fakeReader{msgs: []kafkago.Message{okKept(t, "a")}, end: context.Canceled}
	dlq := &fakeDLQ{}
	ns, _ := run(t, c, reader, dlq)
	if calls != 3 || len(reader.committed) != 1 || len(dlq.got) != 0 {
		t.Fatalf("calls %d committed %d dead letters %d", calls, len(reader.committed), len(dlq.got))
	}
	if len(ns.delays) != 2 || ns.delays[0] != DefaultRetryInitial || ns.delays[1] != 2*DefaultRetryInitial {
		t.Fatalf("backoff = %v", ns.delays)
	}
}

func TestAMessageThatKeepsFailingIsDeadLetteredAfterTheCap(t *testing.T) {
	c := &Consumer{Name: "c", Topic: DeliveryPromiseTopic, Logger: quiet(), Retry: RetryPolicy{Initial: time.Second, Max: 3 * time.Second}}
	calls := 0
	c.Handlers = map[string]HandlerFunc{TypePromiseKept: func(context.Context, ceEvent) error { calls++; return errBoom }}
	msg := okKept(t, "a")
	msg.Partition, msg.Offset = 2, 41
	reader := &fakeReader{msgs: []kafkago.Message{msg}, end: context.Canceled}
	dlq := &fakeDLQ{}
	ns, _ := run(t, c, reader, dlq)
	if calls != maxHandlerAttempts || len(dlq.got) != 1 || len(reader.committed) != 1 {
		t.Fatalf("calls %d dead letters %d committed %d", calls, len(dlq.got), len(reader.committed))
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}; !equalDurations(ns.delays, want) {
		t.Fatalf("backoff = %v, want %v (doubling, capped)", ns.delays, want)
	}
	dl := dlq.got[0]
	h := map[string]string{}
	for _, x := range dl.Headers {
		h[x.Key] = string(x.Value)
	}
	if dl.Topic != DeliveryPromiseTopic+DLQSuffix || string(dl.Key) != "k" || string(dl.Value) != string(msg.Value) ||
		h["x-dlq-source-topic"] != DeliveryPromiseTopic || h["x-dlq-source-partition"] != "2" || h["x-dlq-source-offset"] != "41" ||
		h["x-dlq-error"] != errBoom.Error() || h["x-dlq-failed-at"] == "" {
		t.Fatalf("dead letter = %+v headers %v", dl, h)
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAPoisonMessageIsDeadLetteredAtOnceAndTheNextOneFlows(t *testing.T) {
	r := newRig()
	bad := kafkago.Message{Topic: DeliveryPromiseTopic, Value: event(t, "bad", TypePromiseKept, map[string]any{"deliveredAt": at(1)})}
	reader := &fakeReader{msgs: []kafkago.Message{bad, okKept(t, "good")}, end: context.Canceled}
	dlq := &fakeDLQ{}
	ns, _ := run(t, r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers), reader, dlq)
	if len(dlq.got) != 1 || len(ns.delays) != 0 || len(reader.committed) != 2 || counted(t, r, "n1:30d") != 1 {
		t.Fatalf("dead letters %d sleeps %d committed %d", len(dlq.got), len(ns.delays), len(reader.committed))
	}
}

func TestAFailingDeadLetterPublishAndCommitAreRetriedNeverSkipped(t *testing.T) {
	r := newRig()
	bad := kafkago.Message{Topic: DeliveryPromiseTopic, Value: event(t, "bad", TypePromiseKept, map[string]any{})}
	reader := &fakeReader{msgs: []kafkago.Message{bad}, commitErr: []error{errBoom}, end: context.Canceled}
	dlq := &fakeDLQ{errs: []error{errBoom, errBoom}}
	ns, _ := run(t, r.consumer(DeliveryPromiseTopic, DeliveryPromiseHandlers), reader, dlq)
	if len(dlq.got) != 1 || len(reader.committed) != 1 || len(ns.delays) != 3 {
		t.Fatalf("dead letters %d committed %d sleeps %v", len(dlq.got), len(reader.committed), ns.delays)
	}
}

func TestShutdownNeverDeadLettersAMessageInFlight(t *testing.T) {
	c := &Consumer{Name: "c", Topic: "t", Logger: quiet()}
	ctx, cancel := context.WithCancel(context.Background())
	c.Handlers = map[string]HandlerFunc{TypePromiseKept: func(context.Context, ceEvent) error { cancel(); return errBoom }}
	reader := &fakeReader{msgs: []kafkago.Message{okKept(t, "a")}, end: context.Canceled}
	dlq := &fakeDLQ{}
	c.Reader, c.DLQ, c.sleep = reader, dlq, (&noSleep{}).sleep
	if err := c.Run(ctx); !errors.Is(err, context.Canceled) || len(dlq.got) != 0 || len(reader.committed) != 0 {
		t.Fatalf("err %v dead letters %d committed %d", err, len(dlq.got), len(reader.committed))
	}
}

func TestSleepIsCancellable(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if (RetryPolicy{}).withDefaults().Max != DefaultRetryMax || (RetryPolicy{}).withDefaults().next(4*time.Second) != DefaultRetryMax {
		t.Fatal("defaults")
	}
}

func TestADeadLetterWaitsForANewTopicToGetALeader(t *testing.T) {
	notReady := kafkago.WriteErrors{kafkago.UnknownTopicOrPartition, kafkago.LeaderNotAvailable}
	dlq := &fakeDLQ{errs: []error{notReady, kafkago.LeaderNotAvailable}}
	if err := writeDLQ(context.Background(), dlq, kafkago.Message{Topic: "t.dlq"}, time.Millisecond); err != nil || len(dlq.got) != 1 {
		t.Fatalf("err %v written %d", err, len(dlq.got))
	}
	other := kafkago.WriteErrors{kafkago.UnknownTopicOrPartition, errBoom}
	if err := writeDLQ(context.Background(), &fakeDLQ{errs: []error{other}}, kafkago.Message{}, time.Millisecond); err == nil || !strings.Contains(err.Error(), errBoom.Error()) {
		t.Fatalf("a different error is returned at once: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeDLQ(ctx, &fakeDLQ{errs: []error{kafkago.LeaderNotAvailable, kafkago.LeaderNotAvailable}}, kafkago.Message{}, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait stops: %v", err)
	}
	tries := &fakeDLQ{}
	for range dlqTopicReadyAttempts + 1 {
		tries.errs = append(tries.errs, kafkago.LeaderNotAvailable)
	}
	if err := writeDLQ(context.Background(), tries, kafkago.Message{}, time.Nanosecond); !errors.Is(err, kafkago.LeaderNotAvailable) || len(tries.errs) != 1 {
		t.Fatalf("the wait is bounded: %v (%d left)", err, len(tries.errs))
	}
	if isTopicNotReady(kafkago.WriteErrors{}) || isTopicNotReady(errBoom) {
		t.Fatal("empty or foreign errors are not 'not ready'")
	}
}

func TestReaderConfigAndCloseReleaseEverything(t *testing.T) {
	cfg := readerConfig([]string{"b:1"}, "topic", "group-x")
	if cfg.Topic != "topic" || cfg.GroupID != "group-x" || cfg.CommitInterval != 0 {
		t.Fatalf("cfg = %+v", cfg)
	}
	reader, dlq := &fakeReader{}, &fakeDLQ{}
	if err := (&Consumer{Reader: reader, DLQ: dlq}).Close(); err != nil || !reader.closed || !dlq.closed {
		t.Fatal("Close must release the reader and the writer")
	}
	if defaultLogger(nil) == nil {
		t.Fatal("logger")
	}
}
