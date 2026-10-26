# Runtime: configuration, probes, shutdown, workers and the outbox

`cmd/api` is the only binary. It runs on real PostgreSQL and real Kafka; there is no
alternative runtime and no log sink. Next to the REST API it runs six Kafka
consumers, a recompute scheduler and the outbox relay.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the service's pool. A boot error names it when unset. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy). |
| `KAFKA_BROKERS` | required | Comma-separated `host:port` list (consumers, relay and `/readyz`). A boot error names it when unset or blank. |
| `VENDOR_CONNECTIVITY_CONSUMER_GROUP` | required | Stable group id of the consumer of `retailnetwork.vendor-connectivity.events`. |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | required | Group of the consumer of `retailnetwork.shipping-and-tracking.events`. |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | required | Group of the consumer of `retailnetwork.delivery-promise.events`. |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | required | Group of the consumer of `retailnetwork.offer-availability.events`. |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | required | Group of the consumer of `retailnetwork.node-topology.events`. |
| `SELLER_INBOUND_CONSUMER_GROUP` | required | Group of the consumer of `retailnetwork.seller-inbound.events` (the shipped instant of each inbound shipment, ADR 0009). |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `RECOMPUTE_INTERVAL` | `1m` | How often the scheduler recomputes the due scorecards (Go duration or seconds). |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited (ADR 0005). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `OTEL_SERVICE_NAME` | `node-performance` | `service.name` of spans and metrics (ADR 0010). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC, `host:port` or URL; export never blocks. |
| `SERVICE_VERSION` / `ENVIRONMENT` | `dev` / `local` | `service.version` / `deployment.environment.name`. |

Configuration errors are raised before anything is opened or bound. A group id is
never derived from the pod name: it is a chart value.

## Boot and shutdown

1. Validate the environment. 2. Run the embedded migrations and ping Postgres, both with
exponential backoff (5 attempts, 1 + 2 + 4 + 8 s). 3. Start the six consumers, the
recompute scheduler and the outbox relay (none dials at construction; a broker that is
down only makes fetches and passes fail until it is back). 4. Serve.

On SIGINT/SIGTERM: `/readyz` flips to 503 `not_ready`, wait `SHUTDOWN_DRAIN_DELAY`, drain
HTTP (10 s), stop and await the consumers and the scheduler (10 s), stop and await the
relay LAST (10 s) so nothing they committed is stranded, close the pool. If any worker
returns on its own the process exits non-zero so the orchestrator restarts it. The sum
of the bounds is 5 + 10 + 10 + 10 = 35 s: the chart's `terminationGracePeriodSeconds`
must exceed it.

## Probes

- `GET /healthz` is liveness: 200 `{"status":"ok"}` while the process serves, even while
  draining or while a dependency is down.
- `GET /readyz` is 200 `{"status":"ready"}` only if the drain gate is open, Postgres
  answers a ping and a Kafka broker answers a metadata request. The request names no
  topics (an empty, non-nil list), so it never creates a topic and does not need the
  output topic to exist; a fresh connection is dialled on every check. While draining it
  answers 503 `{"status":"not_ready"}`; while a dependency is down, 503
  `application/problem+json` of type `dependency-unavailable` naming it. Verdicts are
  cached 3 s.

## Consumers and the recompute scheduler

- Each consumer fetches one message at a time and commits its offset only after the
  intake use case returned. The use case claims the CloudEvents id in `processed_events`
  in the same transaction as its effect, so a redelivery changes nothing.
- A transient failure (database down, purchase order facts not arrived yet) is retried up
  to 5 attempts with a capped backoff (200 ms doubling to 5 s) and then published to
  `<topic>.dlq` with `x-dlq-*` headers; a payload that can never be applied is published
  there at once. A message that is not a CloudEvents 1.0 event, or of an unhandled type, is
  skipped.
- The scheduler runs `Recompute` once at boot and then every `RECOMPUTE_INTERVAL`: it
  loads the scorecards that are due (an observation was recorded since the last recompute,
  or one has left the window), recomputes them and appends `NodeScorecardUpdated` / `NodeThresholdBreached` to
  the outbox in the same transaction. A failed round is logged and retried at the next tick.

## The outbox in operation

- Events are written in the same transaction as the aggregates and published later, so a
  write is accepted while Kafka is down; `outbox_events.published_at IS NULL` rows wait and
  are published, in id order, when the broker is back.
- Delivery is at-least-once with a stable CloudEvents `id`; consumers deduplicate.
- A row that failed `OUTBOX_MAX_ATTEMPTS` times is skipped (logged once at ERROR with id,
  topic, type and key) and stays in the table with `attempts` and `last_error`. Fix the
  cause, then re-arm it: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. While a
  row is skipped, later events of the same scorecard can overtake it.
- `idempotency_keys` exists for a future write route; no route uses it today.

## Deployment (Helm)

`charts/node-performance/` deploys the `api` and nothing else (no MCP, frontend, projector or reports).
`values.yaml` documents every key; each `config.*` value maps 1:1 to one variable of the table above, and the
chart ConfigMap (mounted with `envFrom`, with a `checksum/config` pod annotation) carries every one of them except the two
DSNs.

- Required, refused at render with the value named: `database.url` or `database.existingSecret` (`DATABASE_URL`
  from a `secretKeyRef`; `MIGRATIONS_DATABASE_URL` from `database.migrationsExistingSecretKey` with `optional: true`,
  the binary falls back to `DATABASE_URL`), `kafka.brokers`, and `config.vendorConnectivityConsumerGroup`,
  `shippingAndTrackingConsumerGroup`, `deliveryPromiseConsumerGroup`, `offerAvailabilityConsumerGroup`,
  `nodeTopologyConsumerGroup`.
- Observability (ADR 0010): `otel.enabled`, `otel.endpoint`, `otel.serviceName` render `OTEL_EXPORTER_OTLP_ENDPOINT` and
  `OTEL_SERVICE_NAME` (`<name>-mcp` for the mcp component); `SERVICE_VERSION` is `image.tag` or the appVersion, `ENVIRONMENT` is
  `environment`. `extraEnv` may not set any of the four.
- Also refused: a group id that is an env expansion (`$(POD_NAME)`), any `*_MODE` or chart-managed variable in `extraEnv`,
  a `config.httpAddr` port that is not `service.targetPort`, and a `terminationGracePeriodSeconds` below
  `config.shutdownDrainDelay` + 35 s (the sum of the bounds above plus 5 s of margin; 40 s with the default drain).
- Pod: uid 1000, `runAsNonRoot`, read-only root filesystem, no privilege escalation, all capabilities dropped,
  seccomp `RuntimeDefault`; startup and liveness on `/healthz`, readiness on `/readyz`.
- HPA, Ingress and Gateway API HTTPRoute are off by default. More than one replica is safe by design (the relay claims with
  `FOR UPDATE SKIP LOCKED`, consumers share one group per topic) but has not been load-tested.
- Tests: `bash charts/node-performance/tests/render_test.sh` and
  `python3 charts/node-performance/tests/test_service_selectors.py`, both run by the `chart-tests` CI job;
  `helm-lint` (pull requests) runs `ct lint` and the selector test. The `release` job packages
  `node-performance-<version>.tgz`.

## Tests

`make check` (format, vet, build, lint, unit + handler + BDD tests), `make coverage` (90%
over `internal/domain` and `internal/application`), `make mutation` (gremlins per domain
package), `make arch-test` (fitness), `make integration` (testcontainers Postgres + Kafka:
repositories, Drain, the relay, the consumers and the real-wiring test
`cmd/api/e2e_integration_test.go`; needs Docker).
