---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
description: "Operating Node Performance: deployment, Helm chart, probes, migrations, Kafka topics and consumer groups, outbox relay, DLQ, the recompute sweeper and routine procedures."
---

# Node Performance runbook

Bounded context **Node Performance** (Supporting, partner tier) measures every node of the retail
network against what it declared and what was asked of it (acknowledgement latency, rejections,
on-time handover against `requiredShipBy`, promise-kept rate and inventory feed freshness) over
rolling 7-day and 30-day windows, and grades the node (`A` to `D` from 50 observations,
`PROVISIONAL` below). It consumes six sibling event topics, keeps two scorecards per node,
publishes `NodeScorecardUpdated` and `NodeThresholdBreached` through a transactional outbox and
serves two read-only REST routes. This repository is a domain-driven design study; it is not
associated with any company (`README.md` disclaimer). Every variable is in
[Configuration](/docs/operations/configuration); the symptom-driven view is
[Troubleshooting](/docs/operations/troubleshooting).

## Runtime shape

Three binaries, one chart (`charts/node-performance/`), three independent Deployments:

| Binary | Role | Port | Chart switch | Kong route (kind cluster) |
| --- | --- | --- | --- | --- |
| `cmd/api` | REST, six consumers, recompute scheduler, outbox relay (ONE process) | 8080 (Service 80) | always on | `/api/node-performance` (prefix stripped) |
| `cmd/mcp` | read-only MCP, Streamable HTTP on `/mcp` and `/` | 8090 | `mcp.enabled` (off by default) | `/api/mcp/node-performance` (rewritten to `/mcp`) |
| `cmd/a2a` | read-only A2A agent, JSON-RPC on `/` and `/a2a`, Agent Card at `/.well-known/agent-card.json` | 8093 | `a2a.enabled` (off by default) | `/api/a2a/node-performance` (rewritten to `/`) |

warehouse-infra (`helm-values/node-performance.yaml` on develop) switches `mcp`, `a2a` and the
`frontend` (the `node_performance_mfe` console remote) on, with their routes. `cmd/mcp` and
`cmd/a2a` read the same Postgres through the same two read use cases; they never start a worker,
never dial Kafka and cannot write.

Inside the `api` process (`cmd/api/run.go`, `cmd/api/workers.go`):

- **REST** on `HTTP_ADDR` (default `:8080`): chi, `GET /scorecards/{nodeId}`,
  `GET /scorecards?grade=&window=&limit=&after=`, plus the probes. Reads only, no
  authentication, no state-changing route (`internal/adapters/inbound/http`).
- **Six Kafka consumers** (vendor-connectivity, shipping-and-tracking, delivery-promise,
  offer-availability, node-topology, seller-inbound), one message at a time, offset committed
  only after the intake use case returned (`internal/adapters/inbound/kafka/consumer.go`). No
  sibling REST calls: facts arrive only over Kafka ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-facts)).
- **Recompute scheduler** (the sweeper): runs `Recompute` once at boot, then every
  `RECOMPUTE_INTERVAL` (default 1m). Each round loads up to 200 due scorecards, up to 10 rounds per
  run, recomputes them and appends the events to the outbox in the SAME Postgres transaction
  (`cmd/api/workers.go` `runRecompute`, `internal/application/usecases/recompute.go`). A failed
  round is logged at ERROR and retried at the next tick.
- **Outbox relay**: always on, polls every `OUTBOX_RELAY_INTERVAL` (default 1s), publishes
  unpublished `outbox_events` rows in id order (`internal/adapters/outbound/outbox`,
  `internal/adapters/outbound/kafka`).
- Any worker that returns on its own makes the process run the shutdown sequence and exit
  non-zero, so the orchestrator restarts it (`worker stopped` in the log).
- Graceful shutdown (SIGINT/SIGTERM): `/readyz` flips to 503, wait `SHUTDOWN_DRAIN_DELAY` (5s),
  drain HTTP (10s), stop consumers and scheduler (10s), stop the relay LAST (10s), close the pool:
  35s of bounds, chart `terminationGracePeriodSeconds: 40` ([Runtime](/docs/overview/runtime)).

## Dependencies

- **Postgres**: `DATABASE_URL` is the service's pool (through PgBouncer on the cluster);
  `MIGRATIONS_DATABASE_URL` is a direct DSN for the boot migrations, because golang-migrate holds
  a session advisory lock that breaks behind a transaction-pooling proxy (optional key; the
  binary falls back to `DATABASE_URL`). Both DSNs reach the pod from the Secret
  `node-performance-db` via `secretKeyRef`.
- **Kafka**: `KAFKA_BROKERS` (one broker platform-wide), used by the consumers, the relay and
  the `/readyz` broker check. Topics are auto-created by the first writer; `/readyz` never asks
  for topics.
- Boot is resilient to a slow start: migrations and the Postgres ping retry 5 times (1+2+4+8s
  backoff, `internal/bootretry`); consumers and the relay dial lazily, so a broker that is down at
  boot does not block `listen`. A consumer is less patient than the relay, though: a *refused*
  connection is fatal to its worker (only timeouts, resets and EOFs are retried, up to 10
  consecutive fetches, `internal/adapters/inbound/kafka/kafka.go`). In a real run against a
  closed port the consumers logged `worker stopped ... failed to dial` about 10 s after boot and
  the process exited non-zero, to be restarted by the orchestrator. See
  [Troubleshooting](/docs/operations/troubleshooting#pod-restarts-worker-stopped).

## Migrations

Embedded in the binary (`internal/adapters/outbound/postgres/migrations/`, golang-migrate) and run
at every start of `cmd/api`, `cmd/mcp` and `cmd/a2a`; the advisory lock makes concurrent starts
safe. There is no separate migration job.

| Migration | Creates |
| --- | --- |
| `0001_node_performance_schema` | `scorecards`, `scorecard_observations`, `purchase_order_facts` |
| `0002_outbox_inbox_and_idempotency` | `outbox_events`, `processed_events` (the inbox), `idempotency_keys` (no route uses it) |
| `0003_inbound_read_models` | `inbound_shipments`, `inbound_receipts` ([ADR 0009](/docs/adr/0009-inbound-timeliness-and-accuracy-metrics)) |

Check the state with `SELECT version, dirty FROM schema_migrations;`. A `dirty = true` row means a
migration died half-way: fix the cause, then `migrate force` the version by hand; the service
refuses to boot on a dirty database.

## Kafka topics and consumer groups

| Direction | Topic | Group variable | Platform group id (warehouse-infra) |
| --- | --- | --- | --- |
| consumed | `retailnetwork.vendor-connectivity.events` | `VENDOR_CONNECTIVITY_CONSUMER_GROUP` | `node-performance-vendor-connectivity` |
| consumed | `retailnetwork.shipping-and-tracking.events` | `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | `node-performance-shipping-and-tracking` |
| consumed | `retailnetwork.delivery-promise.events` | `DELIVERY_PROMISE_CONSUMER_GROUP` | `node-performance-delivery-promise` |
| consumed | `retailnetwork.offer-availability.events` | `OFFER_AVAILABILITY_CONSUMER_GROUP` | `node-performance-offer-availability` |
| consumed | `retailnetwork.node-topology.events` | `NODE_TOPOLOGY_CONSUMER_GROUP` | `node-performance-node-topology` |
| consumed | `retailnetwork.seller-inbound.events` | `SELLER_INBOUND_CONSUMER_GROUP` | `node-performance-seller-inbound` |
| published | `retailnetwork.node-performance.events` | none | `NodeScorecardUpdated` only |
| published | `retailnetwork.node-performance.analytics` | none | `NodeThresholdBreached` only |
| dead letters | `<consumed topic>.dlq` | none | written by the consumers |

Group ids are fixed and stable (never the pod name): a rescheduled pod resumes from its committed
offsets, while a brand-new group id re-reads its topic from the first offset. Never change a group
id on a running deployment unless a full replay is wanted; replays are safe because every message is
deduplicated on the CloudEvents `id` in `processed_events`.

## Health

- `GET /healthz`: liveness, 200 `{"status":"ok"}` while the process serves, even while draining
  or while a dependency is down (`internal/adapters/inbound/http/readiness.go`). `cmd/mcp` and
  `cmd/a2a` serve only this probe.
- `GET /readyz` (`cmd/api` only): 200 `{"status":"ready"}` only when the drain gate is open,
  Postgres answers a ping AND a Kafka broker answers a metadata request with an empty, non-nil
  topic list (fresh connection per check, verdicts cached 3s, round bounded 2s). Draining: 503
  `{"status":"not_ready"}`. A dependency down: 503 `application/problem+json` of type
  `dependency-unavailable` naming postgres or kafka (`internal/adapters/inbound/http/problems.go`).
- Chart probes: startup and liveness on `/healthz`, readiness on `/readyz`
  (`charts/node-performance/values.yaml`).
- With **Kafka** down both writers keep working: effects and outbox rows commit in one Postgres
  transaction and `outbox_events.published_at IS NULL` rows queue until the broker returns
  ([ADR 0005](/docs/adr/0005-outbox-relay-and-attempt-cap), asserted by
  `cmd/api/e2e_integration_test.go`). With **Postgres** down nothing persists: REST reads fail,
  `/readyz` flips, consumers cannot claim `processed_events`, recompute rounds log ERROR and retry.

## Outbox relay

- Events are written in the same transaction as the aggregates and published later; delivery is
  at-least-once with a stable CloudEvents `id`; consumers deduplicate.
- A row that failed `OUTBOX_MAX_ATTEMPTS` times (20) is skipped (logged once at ERROR with id,
  topic, type and key) and stays in the table with `attempts` and `last_error`; later events of the
  same scorecard can overtake it. Inspect and re-arm:

  ```sql
  SELECT id, event_id, topic, event_type, attempts, last_error, created_at
    FROM outbox_events WHERE published_at IS NULL AND attempts >= 20 ORDER BY id;
  UPDATE outbox_events SET attempts = 0 WHERE id = <id>;
  ```

  Table defined in `internal/adapters/outbound/postgres/migrations/0002_outbox_inbox_and_idempotency.up.sql`.
- Queue depth: `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;`

## Dead-letter queues

A transient failure (database down, purchase-order facts not arrived yet) is retried up to 5
attempts (`maxHandlerAttempts`, `internal/adapters/inbound/kafka/deadletter.go`) with backoff 200ms
doubling to 5s, then published to `<topic>.dlq`; a payload that can never be applied is dead-lettered
at once. The DLQ message keeps the original key, value and headers and adds `x-dlq-source-topic`,
`x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at`. A message that
is not a CloudEvents 1.0 event, or of an unhandled type, is skipped, not dead-lettered. Nothing in
this repository reads a DLQ back: read `x-dlq-error`, fix the producer or dependency, and
republish the payload to the source topic by hand if it is still wanted.

## Deployment (Helm)

`charts/node-performance/` deploys the `api` by default; `mcp`, `a2a` and `frontend` are opt-in
(`values.yaml` documents every key; each `config.*` value maps 1:1 to one variable, and the chart
ConfigMap, mounted with `envFrom` with a `checksum/config` pod annotation, carries every one of them
except the two DSNs).

- Required, refused at render with the value named: `database.url` or `database.existingSecret`
  (`DATABASE_URL` from a `secretKeyRef`; `MIGRATIONS_DATABASE_URL` from
  `database.migrationsExistingSecretKey` with `optional: true`), `kafka.brokers`, and the six
  `config.*ConsumerGroup` values (`vendorConnectivity`, `shippingAndTracking`, `deliveryPromise`,
  `offerAvailability`, `nodeTopology`, `sellerInbound`).
- Observability ([ADR 0010](/docs/adr/0010-fleet-observability-standard)): `otel.enabled`,
  `otel.endpoint`, `otel.serviceName` render `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME`
  (`<name>-mcp` for mcp, `<name>-a2a` for a2a); `SERVICE_VERSION` is `image.tag` or the appVersion,
  `ENVIRONMENT` is `environment`. `extraEnv` may not set any of the four.
- Also refused: a group id that is an env expansion (`$(POD_NAME)`), any `*_MODE` or chart-managed
  variable in `extraEnv`, a `config.httpAddr` port that is not `service.targetPort`, and a
  `terminationGracePeriodSeconds` below `config.shutdownDrainDelay` + 35 s (40 s with the default).
- `a2a.agentUrl` empty renders `http://<release>-a2a.<namespace>.svc.cluster.local:<service.port>/`
  as `A2A_AGENT_URL`.
- Pod: uid 1000, `runAsNonRoot`, read-only root filesystem, no privilege escalation, all
  capabilities dropped, seccomp `RuntimeDefault`.
- HPA, Ingress and Gateway API HTTPRoute are off by default. More than one `api` replica is safe by
  design (the relay claims with `FOR UPDATE SKIP LOCKED`, consumers share one group per topic) but
  has not been load-tested.
- Tests: `bash charts/node-performance/tests/render_test.sh` and
  `python3 charts/node-performance/tests/test_service_selectors.py`, both run by the `chart-tests`
  CI job; `helm-lint` (pull requests) runs `ct lint` and the selector test. The `release` job
  packages `node-performance-<version>.tgz`; the image is `ghcr.io/iqvo/node-performance`
  ([Before first release](/docs/operations/before-first-release)).

## Alerts and dashboards

Managed in warehouse-infra (`develop`), not in this repository:

- Grafana dashboard uid **`warehouse-node-performance`** (`terraform/dashboards/contexts/node-performance.json`):
  Kong request rate, 5xx ratio and p95 latency for the context's routes; service HTTP RED by
  `service_name=~"node-performance.*"`; the business counter
  `nodeperformance_scorecards_recomputed_total` by `outcome`; Go runtime; Loki panels on
  `{app="node-performance"}`.
- Alert group `warehouse-node-performance` (`terraform/alerting/warehouse-alert-rules.json`):
  `WarehouseKong5xxRatioHigh` (more than 5% 5xx on a Kong route for 5m, warning) and
  `WarehousePodRestarting` (2 or more restarts of a `node-performance-.+` pod in 15m, warning).
- Alert group `warehouse-node-performance-slo`: `WarehouseApiAvailabilityBurnFast` and
  `WarehouseApiLatencyBurnFast` (critical, 2m), `WarehouseApiAvailabilityBurnSlow` and
  `WarehouseApiLatencyBurnSlow` (warning, 15m) over the Kong routes (latency objective: upstream
  under 400 ms).
- There is no alert on the recompute counter, the outbox backlog or consumer lag. Watch
  `outcome="failed"` and the unpublished-outbox query above by hand; a flat `recomputed` rate while
  nodes keep reporting means grading has stalled. See [Observability](/docs/operations/observability).

## Operational actions

- Restart: `kubectl -n warehouse-systems rollout restart deploy/node-performance` (the Deployment name
  is the chart fullname `<release>-node-performance`; `-mcp` and `-a2a` for the others).
- Logs: Grafana Loki, `app=node-performance`; the binaries emit JSON logs on stdout. Worker
  lifecycle lines to grep: `worker running`, `worker stopped`, `recompute round`,
  `consumer configured`, `outbox relay configured`.
- Rollback: redeploy the previous `image.tag` chart value (migrations only add tables; there is no
  down-migration step in the deploy path).
- Scale: see the Helm section; the HPA is off by default.
- Force a recompute: restart the `api` (the scheduler runs once at boot).
- Replay a consumed topic: give the consumer a new group id in the chart (a new group starts at the
  first offset); all effects are idempotent on the CloudEvents `id`.
