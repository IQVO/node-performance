package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	inboundkafka "github.com/claudioed/node-performance/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/node-performance/internal/adapters/outbound/outbox"
	"github.com/claudioed/node-performance/internal/application/usecases"
)

// worker is a started background loop and the channel closed once it returned.
type worker struct {
	name string
	done chan struct{}
}

// start runs loop on ctx in a goroutine. A loop that returns for any reason
// other than the cancellation of ctx is logged at ERROR; the supervisor sees the
// closed done channel and exits the process.
func start(ctx context.Context, logger *slog.Logger, name string, loop func(context.Context) error) *worker {
	w := &worker{name: name, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		logger.Info("worker running", "worker", name)
		if err := loop(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("worker stopped", "worker", name, "error", err)
		}
	}()
	return w
}

// startConsumers starts the six consumers; the readers dial lazily inside Run,
// so a broker outage never blocks boot. The returned close releases readers and
// dead-letter writers and must run after the workers stopped.
func startConsumers(ctx context.Context, cfg config, intake usecases.Intake, logger *slog.Logger) ([]*worker, func()) {
	consumers := inboundkafka.NewConsumers(cfg.brokers, cfg.groups, intake, logger)
	workers := make([]*worker, 0, len(consumers))
	for _, c := range consumers {
		logger.Info("consumer configured", "consumer", c.Name, "topic", c.Topic)
		workers = append(workers, start(ctx, logger, c.Name, c.Run))
	}
	return workers, func() {
		for _, c := range consumers {
			if err := c.Close(); err != nil {
				logger.Error("consumer close failed", "consumer", c.Name, "error", err)
			}
		}
	}
}

// startRecompute starts the scheduler: every interval it recomputes the
// scorecards that are due (ADR 0002, A15). A failed round is logged and retried
// at the next tick; the loop only returns when ctx is done.
func startRecompute(ctx context.Context, interval time.Duration, recompute usecases.Recompute, logger *slog.Logger) *worker {
	return start(ctx, logger, "recompute scheduler", func(ctx context.Context) error {
		return runRecompute(ctx, interval, recompute, logger)
	})
}

func runRecompute(ctx context.Context, interval time.Duration, recompute usecases.Recompute, logger *slog.Logger) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		res, err := recompute.Run(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			logger.Error("recompute round failed", "error", err, "recomputed", res.Recomputed, "events", res.Events)
		case res.Recomputed > 0 || res.Conflicts > 0:
			logger.Info("recompute round", "recomputed", res.Recomputed, "events", res.Events, "conflicts", res.Conflicts)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// startOutboxRelay starts the always-on relay over the Kafka sink. Kafka is
// dialled lazily on the first send, never here, so a broker that is down at boot
// only makes passes fail until it is up. The worker's done channel closes after
// the relay returned and the sink was closed.
func startOutboxRelay(ctx context.Context, store outboxrelay.Store, cfg config, logger *slog.Logger) *worker {
	sink := outboundkafka.NewRelaySink(cfg.brokerList())
	relay := outboxrelay.NewOutboxRelay(store, sink, logger,
		outboxrelay.WithInterval(cfg.relayInterval),
		outboxrelay.WithMaxAttempts(cfg.maxAttempts),
	)
	return start(ctx, logger, "outbox relay", func(ctx context.Context) error {
		defer func() {
			if err := sink.Close(); err != nil {
				logger.Error("kafka relay sink close failed", "error", err)
			}
		}()
		logger.Info("outbox relay configured", "interval", cfg.relayInterval, "max_attempts", cfg.maxAttempts,
			"events_topic", outboundkafka.EventsTopic, "analytics_topic", outboundkafka.AnalyticsTopic)
		return relay.Run(ctx)
	})
}
