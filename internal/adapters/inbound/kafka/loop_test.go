package kafka

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
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

// idleTimeout is the error shape kafka-go surfaces when a long-lived broker
// connection's read deadline fires on an idle topic: *net.OpError with
// Timeout() true.
func idleTimeout() error {
	return &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
}

func TestTransientFetchErrorClassification(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"idle read timeout", idleTimeout(), true},
		{"connection reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, true},
		{"broken pipe", &net.OpError{Op: "write", Net: "tcp", Err: syscall.EPIPE}, true},
		{"clean close", io.EOF, true},
		{"wrapped clean close", fmtErrorw(io.EOF), true},
		{"connection refused stays fatal", refused, false},
		{"canceled context stays fatal", context.Canceled, false},
		{"caller deadline stays fatal", context.DeadlineExceeded, false},
		{"plain error stays fatal", errors.New("kafka: group rebalanced"), false},
		{"nil", nil, false},
	} {
		if got := transientFetchError(c.err); got != c.want {
			t.Fatalf("%s: transientFetchError = %v, want %v", c.name, got, c.want)
		}
	}
}

func fmtErrorw(err error) error { return errors.Join(errors.New("fetch: "), err) }

func TestFetchRetriesStalledBrokerConnectionsAndAResetFollowsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &fakeReader{fetchErrs: []error{idleTimeout(), idleTimeout()}, msgs: []kafkago.Message{okKept(t, "a")}, alwaysErr: idleTimeout()}
	var slept []time.Duration
	handled := 0
	l := &consumeLoop{reader: r, logger: quiet(), name: "t",
		sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			if len(slept) == 3 { // two blips before the message, one after it
				cancel()
				return ctx.Err()
			}
			return nil
		},
		handle: func(context.Context, kafkago.Message) error { handled++; return nil }}
	err := l.run(ctx)
	if !errors.Is(err, context.Canceled) || handled != 1 || len(r.committed) != 1 {
		t.Fatalf("err %v handled %d committed %d", err, handled, len(r.committed))
	}
	// Before the message: backoff doubles from the initial 200ms. After the
	// successful fetch the counter AND the delay reset, so the third sleep is
	// the initial 200ms again, not the doubled 800ms.
	if len(slept) != 3 || slept[0] != 200*time.Millisecond || slept[1] != 400*time.Millisecond || slept[2] != 200*time.Millisecond {
		t.Fatalf("backoff sequence = %v", slept)
	}
}

func TestAPersistentlyStalledBrokerStillFailsTheWorker(t *testing.T) {
	r := &fakeReader{alwaysErr: idleTimeout()}
	sleeps := 0
	l := &consumeLoop{reader: r, logger: quiet(), name: "t",
		sleep:  func(context.Context, time.Duration) error { sleeps++; return nil },
		handle: func(context.Context, kafkago.Message) error { return nil }}
	err := l.run(context.Background())
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() || sleeps != maxTransientFetchFailures {
		t.Fatalf("err %v sleeps %d", err, sleeps)
	}
}

func TestAFatalFetchErrorIsNotRetried(t *testing.T) {
	r := &fakeReader{alwaysErr: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	sleeps := 0
	l := &consumeLoop{reader: r, logger: quiet(), name: "t",
		sleep:  func(context.Context, time.Duration) error { sleeps++; return nil },
		handle: func(context.Context, kafkago.Message) error { return nil }}
	if err := l.run(context.Background()); err == nil || sleeps != 0 {
		t.Fatalf("err %v sleeps %d", err, sleeps)
	}
}
