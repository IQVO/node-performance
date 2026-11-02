package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/node-performance/internal/adapters/inbound/http"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
)

// listDueStub is a ScorecardRepository whose ListDue is scripted; the rest is
// never reached by Recompute when nothing is due.
type listDueStub struct {
	ports.ScorecardRepository
	calls atomic.Int32
	err   error
}

func (s *listDueStub) ListDue(context.Context, time.Time, int) ([]string, error) {
	s.calls.Add(1)
	return nil, s.err
}

func recomputeOver(s *listDueStub) usecases.Recompute {
	return usecases.NewRecompute(usecases.Deps{Scorecards: s, Clock: fixedClock{}})
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) }

// leaseStub is a scripted recomputeLease: held says whether Acquire succeeds,
// and the counters record what the scheduler did with it.
type leaseStub struct {
	held     atomic.Bool
	acquired atomic.Int32
	released atomic.Int32
}

func (l *leaseStub) Acquire(context.Context) (func(), bool, error) {
	l.acquired.Add(1)
	if !l.held.Load() {
		return nil, false, nil
	}
	var once sync.Once
	return func() {
		once.Do(func() { l.released.Add(1) })
	}, true, nil
}

func TestTheSchedulerSkipsRoundsAnotherReplicaHoldsAndRunsUnderTheLease(t *testing.T) {
	logger, logs := quiet()
	stub := &listDueStub{}
	lease := &leaseStub{}
	lease.held.Store(false) // another replica holds it
	ctx, cancel := context.WithCancel(context.Background())
	w := startRecompute(ctx, 5*time.Millisecond, recomputeOver(stub), lease, logger)
	deadline := time.After(5 * time.Second)
	for lease.acquired.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("only %d acquire attempts", lease.acquired.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("a held lease must skip the round, %d rounds ran", stub.calls.Load())
	}
	lease.held.Store(true) // this replica takes over
	for stub.calls.Load() < 1 || lease.released.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("rounds %d releases %d", stub.calls.Load(), lease.released.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if lease.released.Load() > lease.acquired.Load() {
		t.Fatalf("every lease is released once: %d releases of %d acquires", lease.released.Load(), lease.acquired.Load())
	}
	cancel()
	await(logger, w)
	if !strings.Contains(logs.String(), "another replica holds the lease") {
		t.Fatalf("a skipped round is logged:\n%s", logs.String())
	}
}

func TestTheSchedulerRecomputesEveryIntervalUntilItsContextIsDone(t *testing.T) {
	logger, logs := quiet()
	stub := &listDueStub{err: errors.New("db down")}
	ctx, cancel := context.WithCancel(context.Background())
	w := startRecompute(ctx, 5*time.Millisecond, recomputeOver(stub), nil, logger)
	deadline := time.After(5 * time.Second)
	for stub.calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d rounds ran", stub.calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	await(logger, w)
	select {
	case <-w.done:
	default:
		t.Fatal("the scheduler did not stop")
	}
	if !strings.Contains(logs.String(), "recompute round failed") || strings.Contains(logs.String(), "worker stopped") {
		t.Fatalf("a failed round is logged and retried; a cancellation is not an error:\n%s", logs.String())
	}
}

func TestAWorkerThatReturnsByItselfIsLogged(t *testing.T) {
	logger, logs := quiet()
	w := start(context.Background(), logger, "doomed", func(context.Context) error { return errors.New("reader failed") })
	<-w.done
	if !strings.Contains(logs.String(), "worker stopped") || !strings.Contains(logs.String(), "reader failed") {
		t.Fatalf("logs:\n%s", logs.String())
	}
	select {
	case name := <-watchWorkers([]*worker{w}):
		if name != "doomed" {
			t.Fatalf("name = %q", name)
		}
	case <-time.After(time.Second):
		t.Fatal("the watcher did not see the dead worker")
	}
}

func TestConsumersAreBuiltForEveryTopicAndNeverDialAtBoot(t *testing.T) {
	logger, _ := quiet()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config{brokers: []string{"127.0.0.1:1"}}
	workers, closeAll := startConsumers(ctx, cfg, usecases.Intake{}, logger)
	if len(workers) != 6 {
		t.Fatalf("%d consumers", len(workers))
	}
	cancel()
	awaitAll(logger, workers)
	closeAll()
}

func TestReadinessNamesTheDependencyThatFails(t *testing.T) {
	gate := newReadiness(func(context.Context) error { return errors.New("no db") }, []string{"127.0.0.1:1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failing := gate.Failing(ctx)
	if len(failing) != 2 || failing[0] != "postgres" || failing[1] != "kafka" {
		t.Fatalf("failing = %v", failing)
	}
}

func serverOn(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: time.Second}
}

func TestServeStopsInTheFleetOrderOnCancellation(t *testing.T) {
	logger, _ := quiet()
	gate := inboundhttp.NewReadiness(0, 0)
	var order []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveUntilSignal(ctx, logger, serverOn("127.0.0.1:0"), gate, 0, nil, func() { order = append(order, "workers") })
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a graceful shutdown returns nil: %v", err)
	}
	if gate.Ready() || len(order) != 1 {
		t.Fatalf("ready %v order %v: /readyz flips and the workers stop", gate.Ready(), order)
	}
}

func TestServeExitsNonZeroWhenAWorkerDiesOrTheListenerFails(t *testing.T) {
	logger, _ := quiet()
	dead := &worker{name: "outbox relay", done: make(chan struct{})}
	close(dead.done)
	stopped := false
	err := serveUntilSignal(context.Background(), logger, serverOn("127.0.0.1:0"), inboundhttp.NewReadiness(0, 0), 0, []*worker{dead}, func() { stopped = true })
	if err == nil || !strings.Contains(err.Error(), "outbox relay stopped unexpectedly") || !stopped {
		t.Fatalf("err %v stopped %v", err, stopped)
	}
	stopped = false
	err = serveUntilSignal(context.Background(), logger, serverOn("256.0.0.1:99999"), inboundhttp.NewReadiness(0, 0), 0, nil, func() { stopped = true })
	if err == nil || !strings.Contains(err.Error(), "http server") || !stopped {
		t.Fatalf("a listen failure: err %v stopped %v", err, stopped)
	}
}

func TestAwaitGivesUpOnAWorkerThatNeverStops(t *testing.T) {
	logger, logs := quiet()
	stuck := &worker{name: "stuck", done: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		// workerDrainTimeout is 10 s; a worker that is already done returns at once.
		close(stuck.done)
		await(logger, stuck)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("await blocked on a finished worker")
	}
	if strings.Contains(logs.String(), "did not stop") {
		t.Fatalf("logs:\n%s", logs.String())
	}
}
