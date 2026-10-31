# Node Performance runbook

Bounded context **Node Performance** (Supporting, partner tier) measures every node of the retail
network against what it declared and what was asked of it — acknowledgement latency, rejections,
on-time handover against `requiredShipBy`, promise-kept rate and inventory feed freshness — over
rolling 7-day and 30-day windows, and grades the node (`A`–`D` from 50 observations, `PROVISIONAL`
below). It consumes six sibling event topics, keeps two scorecards per node
(`onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`, `promiseKeptRate`,
`feedFreshnessP95Minutes`), publishes `NodeScorecardUpdated` / `NodeThresholdBreached` through a
transactional outbox and serves two read-only REST routes (`README.md`, `apis/openapi.yaml`,
`apis/asyncapi.yaml`). This repository is a domain-driven design study; it is not associated with
any company (`README.md` disclaimer).

## Runtime shape

- One binary, `cmd/api`, deployed as a single `api` Deployment by `charts/node-performance/` (the
  chart deploys nothing else: no MCP, frontend or projector — `docs/runtime.md`).
- Inside the one process (`cmd/api/run.go`, `cmd/api/workers.go`):
  - **REST** on `HTTP_ADDR` (default `:8080`): chi, `GET /scorecards/{nodeId}`,
    `GET /scorecards?grade=&window=&limit=&after=`, plus the probes. Reads only, no
    authentication, no state-changing route (`internal/adapters/inbound/http`).
  - **Six Kafka consumers** (vendor-connectivity, shipping-and-tracking, delivery-promise,
    offer-availability, node-topology, seller-inbound), one message at a time, offset committed only after the
    intake use case returned (`internal/adapters/inbound/kafka/consumer.go`). No sibling REST
    calls: facts arrive only over Kafka (`docs/adr/0003`).
  - **Recompute scheduler**: runs `Recompute` once at boot, then every `RECOMPUTE_INTERVAL`
    (default 1m). Each round loads the scorecards that are due (an observation recorded since the
    last recompute, or one that left the window), recomputes them and appends the events to the
    outbox in the SAME Postgres transaction (`cmd/api/workers.go` `runRecompute`,
    `internal/application/usecases/recompute.go`). A failed round is logged at ERROR and retried
    at the next tick; a scorecard under 50 observations stays `PROVISIONAL` and publishes nothing.
  - **Outbox relay**: always on, polls every `OUTBOX_RELAY_INTERVAL` (default 1s), publishes
    unpublished `outbox_events` rows in id order to `retailnetwork.node-performance.events` and
    `retailnetwork.node-performance.analytics` (`internal/adapters/outbound/outbox`,
    `internal/adapters/outbound/kafka`).
- Any worker that returns on its own makes the process run the shutdown sequence and exit
  non-zero, so the orchestrator restarts it (`cmd/api/run.go`, `cmd/api/workers.go`).
- Graceful shutdown (SIGINT/SIGTERM): `/readyz` flips to 503, wait `SHUTDOWN_DRAIN_DELAY` (5s),
  drain HTTP (10s), stop consumers and scheduler (10s), stop the relay LAST (10s), close the pool
  — 35s of bounds, chart `terminationGracePeriodSeconds: 40` (`docs/runtime.md`).

## Dependencies

- **Postgres** — `DATABASE_URL` is the service's pool (reached through PgBouncer on the cluster);
  `MIGRATIONS_DATABASE_URL` is a direct DSN for the boot migrations, because golang-migrate holds
  a session advisory lock that breaks behind a transaction-pooling proxy (optional key; the
  binary falls back to `DATABASE_URL`). Both DSNs reach the pod from the Secret
  `node-performance-db` via `secretKeyRef` (`charts/node-performance/templates/deployment.yaml`,
  `internal/adapters/outbound/postgres/config.go`). Embedded migrations:
  `internal/adapters/outbound/postgres/migrations/`.
- **Kafka** — `KAFKA_BROKERS`, comma-separated `host:port`, used by the consumers, the relay and
  the `/readyz` broker check. Topics are auto-created by the first writer; `/readyz` never asks
  for topics. Consumed: `retailnetwork.vendor-connectivity.events`,
  `retailnetwork.shipping-and-tracking.events`, `retailnetwork.delivery-promise.events`,
  `retailnetwork.offer-availability.events`, `retailnetwork.node-topology.events`.
- Boot is resilient: migrations and the Postgres ping retry 5 times (1+2+4+8s backoff,
  `internal/bootretry`); consumers and the relay dial lazily, so a broker down at boot never
  blocks start (`docs/runtime.md`).

## Health

- `GET /healthz` — liveness: 200 `{"status":"ok"}` while the process serves, even while draining
  or while a dependency is down (`internal/adapters/inbound/http/readiness.go`).
- `GET /readyz` — 200 `{"status":"ready"}` only when the drain gate is open, Postgres answers a
  ping AND a Kafka broker answers a metadata request with an empty, non-nil topic list (fresh
  connection per check, verdicts cached 3s). Draining: 503 `{"status":"not_ready"}`. A dependency
  down: 503 `application/problem+json` of type `dependency-unavailable` naming postgres or kafka
  (`internal/adapters/inbound/http/problems.go`).
- Chart probes: startup and liveness on `/healthz`, readiness on `/readyz`
  (`charts/node-performance/values.yaml`).
- Writes while a dependency is down: the REST surface is read-only, so the only writers are the
  consumers' intake and the recompute. With **Kafka** down both keep working — effects and outbox
  rows commit in one Postgres transaction and `outbox_events.published_at IS NULL` rows queue
  until the broker returns (`docs/adr/0005`, asserted by `cmd/api/e2e_integration_test.go`).
  With **Postgres** down nothing persists: REST reads fail, `/readyz` flips, consumers cannot
  claim `processed_events`, recompute rounds log ERROR and retry.

## Configuration

Every variable `cmd/api` reads (`cmd/api/config.go`; a missing mandatory one is a boot error
naming it; each `config.*` chart value maps 1:1 to one variable, `charts/node-performance/values.yaml`;
the ConfigMap carries all of them except the two DSNs):

| Env var | Default | Chart value | Meaning |
| --- | --- | --- | --- |
| `DATABASE_URL` | required | `database.url` / `database.existingSecret` (key `DATABASE_URL`) | pooled Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | `database.migrationsExistingSecretKey` | direct DSN for the boot migrations |
| `KAFKA_BROKERS` | required | `kafka.brokers` | comma-separated `host:port` list |
| `VENDOR_CONNECTIVITY_CONSUMER_GROUP` | required | `config.vendorConnectivityConsumerGroup` | group id for the vendor-connectivity topic |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | required | `config.shippingAndTrackingConsumerGroup` | group id for the shipping-and-tracking topic |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | required | `config.deliveryPromiseConsumerGroup` | group id for the delivery-promise topic |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | required | `config.offerAvailabilityConsumerGroup` | group id for the offer-availability topic |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | required | `config.nodeTopologyConsumerGroup` | group id for the node-topology topic |
| `SELLER_INBOUND_CONSUMER_GROUP` | required | `config.sellerInboundConsumerGroup` | group id for the seller-inbound topic |
| `HTTP_ADDR` | `:8080` | `config.httpAddr` | listen address; port must equal `service.targetPort` |
| `RECOMPUTE_INTERVAL` | `1m` | `config.recomputeInterval` | recompute scheduler tick (Go duration or plain seconds) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | `config.outboxRelayInterval` | relay poll interval |
| `OUTBOX_MAX_ATTEMPTS` | `20` | `config.outboxMaxAttempts` | failures before the relay excludes a row; `0` = unlimited |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | `config.shutdownDrainDelay` | wait after flipping `/readyz`; `0` disables |
| `LOG_LEVEL` | `info` | `config.logLevel` | `debug`/`info`/`warn`/`error` |

The chart refuses to render without the DSN source, `kafka.brokers` and the six group ids, and
refuses a `terminationGracePeriodSeconds` below `config.shutdownDrainDelay` + 35s
(`charts/node-performance/templates/_helpers.tpl`).

## Alerts and dashboards

- Traffic enters through the Kong route `/api/node-performance` (prefix stripped) on the local
  kind cluster; it fronts the chart's Service.
- Grafana dashboard uid **`warehouse-node-performance`**; pod-restart alerts for the Deployment
  exist (both managed in warehouse-infra, not this repo).
- No business metrics are instrumented yet, so business-metric panels on the dashboard stay empty
  until the service instruments itself: an empty panel is not an outage signal. Verify health via
  `/healthz`, `/readyz` and logs instead. This runbook deliberately states no SLOs.

## Failure modes

### Pod crash-loop
- Symptom: `CrashLoopBackOff`, restarts climbing, pod-restart alert fires.
- Diagnosis: `kubectl -n warehouse-systems logs deploy/node-performance --previous`. A boot error
  names the missing variable (raised before anything is opened — `cmd/api/config.go`); a
  past-boot exit has `worker stopped` naming the loop that returned (the non-zero exit is by
  design). Boot tolerates ~15s of Postgres downtime (5 retries) inside the ~60s startup probe.
- Action: fix the named env/Secret/chart value or its dependency, then
  `kubectl -n warehouse-systems rollout restart deploy/node-performance`.

### Postgres unreachable
- Symptom: `/readyz` 503 `dependency-unavailable` naming postgres; the pod leaves Service
  endpoints; REST reads fail; consumer handler retries in the logs.
- Diagnosis: `kubectl -n warehouse-systems exec deploy/node-performance -- wget -qO- http://localhost:8080/readyz`
  (from another pod if restarting); check Postgres and PgBouncer.
- Action: restore the dependency; recovery is automatic (verdicts cached 3s). No data loss:
  offsets commit only after the intake returned, and the CloudEvents id is claimed in
  `processed_events` in the same transaction as the effect, so a redelivery changes nothing.

### Kafka unreachable
- Symptom: `/readyz` 503 naming kafka while `/healthz` stays 200 and REST reads stay 200 (they
  only touch Postgres); relay ERROR logs; the unpublished outbox tail grows.
- Diagnosis: `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;`
- Action: restore the broker; no operator action otherwise. Writes (scorecard recompute +
  outbox rows, one transaction) keep being accepted and are published in id order once the broker
  is back (`docs/adr/0005`).

### Outbox relay stuck / a row parked after max attempts
- Symptom: one ERROR log naming `outbox row <id> (<type>)` with id, topic, type and key; that row
  stays unpublished and later events of the same scorecard can overtake it.
- Diagnosis: the relay stops claiming rows whose `attempts` reached `OUTBOX_MAX_ATTEMPTS` (20 by
  default) — `internal/adapters/outbound/postgres/relay_store.go`. Inspect:
  `SELECT id, event_id, topic, event_type, attempts, last_error, created_at FROM outbox_events WHERE published_at IS NULL AND attempts >= 20 ORDER BY id;`
  (table defined in `internal/adapters/outbound/postgres/migrations/0002_outbox_inbox_and_idempotency.up.sql`).
- Action: read `last_error`, fix the cause (usually the broker), then re-arm the row:
  `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` — the next relay pass claims it again.

### Consumer lag or retry logs
- Symptom: repeated `handling failed ... retrying` logs on one consumer; lag grows; eventually
  `handling failed after the maximum attempts; dead-lettering the message`.
- Diagnosis: a transient failure (database down, purchase-order facts not arrived yet) is retried
  up to 5 attempts (`maxHandlerAttempts`, `internal/adapters/inbound/kafka/deadletter.go`) with
  backoff 200ms doubling to 5s (`internal/adapters/inbound/kafka/kafka.go`), then published to
  `<topic>.dlq` with `x-dlq-source-topic/-partition/-offset`, `x-dlq-error` and `x-dlq-failed-at`
  headers; a payload that can never be applied is dead-lettered at once. A consumed topic whose
  producer has not written yet only shows fetch retries — topics are auto-created by the first
  writer, so this is normal for a not-yet-integrated sibling, not a fault.
- Action: read `x-dlq-error` on the DLQ topic, fix the producer or dependency, redeliver manually
  if needed. Non-CloudEvents or unhandled-type messages are skipped by design.

### Recompute sweep not updating KPIs
- Symptom: scorecards stale in REST reads; no `NodeScorecardUpdated` events.
- Diagnosis: a `recompute round failed` ERROR (with recomputed/events counts) means every round
  throws and retries next tick — usually Postgres. Otherwise check the due-condition: only
  scorecards with an observation recorded since the last recompute (or one that left the window)
  are recomputed, and below 50 observations a scorecard is `PROVISIONAL` and publishes nothing.
- Action: fix the logged cause; the next tick logs `recompute round` with `recomputed > 0`. If
  events are appended but not published, the relay is the problem (see above).

## Operational actions

- Restart: `kubectl -n warehouse-systems rollout restart deploy/node-performance`
  (Deployment name is the chart fullname `<release>-node-performance`).
- Logs: Grafana Loki, `app=node-performance`; the binary emits JSON logs on stdout
  (`cmd/api/run.go`). Worker lifecycle lines to grep: `worker running`, `worker stopped`,
  `recompute round`, `consumer configured`, `outbox relay configured`.
- Rollback: redeploy the previous `image.tag` chart value.
- Scale: more than one replica is safe by design (the relay claims rows with
  `FOR UPDATE SKIP LOCKED`, consumers share one stable group per topic, aggregate writes are
  version-guarded) but has not been load-tested; the HPA is off by default
  (`charts/node-performance/values.yaml`).
