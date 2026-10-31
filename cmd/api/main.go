// Command api is node-performance's composition root: it wires the environment
// into the Postgres and Kafka adapters, the adapters into the use cases, and the
// use cases into the REST router, the five Kafka consumers, the recompute
// scheduler and the outbox relay, all on real Postgres and real Kafka. There is
// no alternative runtime, no log sink and no consumer or relay that is off.
//
// Environment:
//
//	DATABASE_URL                       Postgres DSN (REQUIRED)
//	MIGRATIONS_DATABASE_URL            direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS                      comma-separated brokers (REQUIRED: relay, consumers, /readyz)
//	VENDOR_CONNECTIVITY_CONSUMER_GROUP stable group ids, one per consumed topic
//	SHIPPING_AND_TRACKING_CONSUMER_GROUP   (ALL REQUIRED: a boot error names the variable)
//	DELIVERY_PROMISE_CONSUMER_GROUP
//	OFFER_AVAILABILITY_CONSUMER_GROUP
//	NODE_TOPOLOGY_CONSUMER_GROUP
//	HTTP_ADDR                          listen address (default :8080)
//	RECOMPUTE_INTERVAL                 how often due scorecards are recomputed (Go duration or seconds, default 1m)
//	OUTBOX_RELAY_INTERVAL              relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS                failures before the relay excludes a row (default 20, 0 = unlimited)
//	SHUTDOWN_DRAIN_DELAY               wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                          debug|info|warn|error (default info)
//	OTEL_SERVICE_NAME                  service.name of spans and metrics (default node-performance)
//	OTEL_EXPORTER_OTLP_ENDPOINT        Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION                    service.version (default dev)
//	ENVIRONMENT                        deployment.environment.name (default local)
//
// GET /readyz is 200 only while Postgres answers a ping AND a Kafka broker
// answers a metadata request; /healthz is liveness only. The process exits
// non-zero when the relay, a consumer or the scheduler returns on its own, so
// the orchestrator restarts it.
package main

import (
	"context"
	"log/slog"
	"os"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}
