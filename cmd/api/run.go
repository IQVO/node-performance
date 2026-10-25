package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/node-performance/internal/adapters/inbound/http"
	"github.com/claudioed/node-performance/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/bootretry"
)

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: newLogLevel(os.Getenv(envLogLevel))}))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	cfg, err := loadConfig(os.Getenv, logger)
	if err != nil {
		return err
	}
	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after the
	// HTTP drain, the consumers and the relay have all stopped.
	ad, closeAdapters, err := buildAdapters(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	readiness := newReadiness(ad.pool.Ping, cfg.brokers)
	handler, err := inboundhttp.NewRouter(buildServer(ad, readiness, logger))
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: cfg.addr, Handler: handler, ReadHeaderTimeout: defaultReadHeaderLimit}

	// Two lifetimes: the consumers and the scheduler stop first, the relay last,
	// so nothing they committed is stranded in the outbox.
	workCtx, stopWork := context.WithCancel(context.Background())
	defer stopWork()
	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()

	consumers, closeConsumers := startConsumers(workCtx, cfg, usecases.NewIntake(ad.deps), logger)
	work := make([]*worker, 0, len(consumers)+1)
	work = append(work, consumers...)
	work = append(work, startRecompute(workCtx, cfg.recomputeInterval, usecases.NewRecompute(ad.deps), logger))
	relay := startOutboxRelay(relayCtx, ad.relayStore, cfg, logger)

	all := make([]*worker, 0, len(work)+1)
	all = append(append(all, work...), relay)
	return serveUntilSignal(ctx, logger, httpServer, readiness, cfg.drainDelay, all, func() {
		stopWork()
		awaitAll(logger, work)
		closeConsumers()
		stopRelay()
		await(logger, relay)
	})
}

// newReadiness builds the /readyz gate: the drain gate plus a Postgres ping and
// a Kafka broker answering a topic-less metadata request (never topics: the
// broker auto-creates them on first write), cached 3 s, bounded 2 s. The ping is
// a parameter so the gate is testable without a pool.
func newReadiness(ping func(context.Context) error, brokers []string) *inboundhttp.Readiness {
	return inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(brokers).Check},
	)
}

// adapters is the set of outbound adapters the composition root wires.
type adapters struct {
	pool       *pgxpool.Pool
	relayStore *postgres.RelayStore
	deps       usecases.Deps
}

// buildAdapters runs the embedded migrations and wires the Postgres adapters.
// The first outbound dial of an injected pod can be reset, so the migrations and
// the first ping retry with backoff; on exhaustion the LAST error is returned
// and the process refuses to boot.
func buildAdapters(ctx context.Context, cfg config, logger *slog.Logger) (adapters, func(), error) {
	noop := func() {}
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(cfg.migrationsURL)
	}); err != nil {
		return adapters{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return adapters{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return adapters{}, noop, err
	}
	logger.Info("postgres adapters configured")
	return postgresAdapters(pool), pool.Close, nil
}

func postgresAdapters(pool *pgxpool.Pool) adapters {
	return adapters{pool: pool, relayStore: postgres.NewRelayStore(pool), deps: usecases.Deps{
		Scorecards: postgres.NewScorecardRepo(pool), Facts: postgres.NewPurchaseOrderFactsRepo(pool), Inbound: postgres.NewInboundRepo(pool),
		Inbox: postgres.NewInboxRepo(pool), Outbox: postgres.NewOutboxRepo(pool, outboundkafka.NewEncoder()),
		UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}}
}

// buildServer wires the query use cases into the REST server.
func buildServer(ad adapters, readiness *inboundhttp.Readiness, logger *slog.Logger) *inboundhttp.Server {
	q := usecases.Queries{Reader: postgres.NewReader(ad.pool), Inbound: postgres.NewInboundRepo(ad.pool), Clock: clock.System{}}
	return &inboundhttp.Server{GetScorecard: q, ListScorecards: q, Readiness: readiness, Logger: logger}
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM, ctx cancellation or a
// listen error, then shuts down in the fleet order: (1) /readyz flips to 503,
// (2) wait the drain delay, (3) drain HTTP, (4) stop the workers (consumers and
// scheduler first, the relay last, inside stopWorkers); the caller's deferred
// pool.Close runs after all of it. When a worker returns on its own the service
// cannot run: it exits non-zero so the orchestrator restarts it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	drainDelay time.Duration, workers []*worker, stopWorkers func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	died := watchWorkers(workers)
	select {
	case err := <-errCh:
		stopWorkers()
		return fmt.Errorf("http server: %w", err)
	case name := <-died:
		stopWorkers()
		_ = httpServer.Close()
		return fmt.Errorf("%s stopped unexpectedly: exiting so the orchestrator restarts the service", name)
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if drainDelay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", drainDelay.String())
		time.Sleep(drainDelay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)
	stopWorkers()
	return err
}

// watchWorkers returns a channel that receives the name of the first worker that
// returns. Workers only return when stopped or on an unrecoverable failure, so a
// value read while the service is serving means a worker died.
func watchWorkers(workers []*worker) <-chan string {
	died := make(chan string, len(workers))
	for _, w := range workers {
		go func() {
			<-w.done
			died <- w.name
		}()
	}
	return died
}

// awaitAll waits (bounded) for every worker to return.
func awaitAll(logger *slog.Logger, workers []*worker) {
	for _, w := range workers {
		await(logger, w)
	}
}

// await waits (bounded) for w to return.
func await(logger *slog.Logger, w *worker) {
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}
