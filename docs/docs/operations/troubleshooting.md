---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
description: "Symptom, cause, check and fix for the real failure modes of Node Performance: boot errors, readiness, consumers, DLQ, outbox, grading and the read-only agents."
---

# Troubleshooting

Each entry is symptom, cause, check, fix, taken from the code and from real runs of the
binaries. Configuration names are in [Configuration](/docs/operations/configuration), the
operating view in the [Runbook](/docs/operations/runbook), the log messages in
[Observability](/docs/operations/observability).

## Boot

### Process exits at once with `missing required environment variable: X`

- **Cause**: `loadConfig` (`cmd/api/config.go`) refuses to start without `DATABASE_URL`,
  `KAFKA_BROKERS` and the six `*_CONSUMER_GROUP` variables. It reports the first missing one, in
  that order, so fixing one reveals the next.
- **Check**: the last log line, for example (real output):
  `{"level":"ERROR","msg":"service exited with error","error":"missing required environment variable: DATABASE_URL"}`.
- **Fix**: set the variable; in the chart set `database.*`, `kafka.brokers` and the six
  `config.*ConsumerGroup` values (the chart refuses to render without them).

### `cmd/a2a` exits with `A2A_AGENT_URL is required` or `... is not an absolute http(s) URL`

- **Cause**: the Agent Card advertises `A2A_AGENT_URL`; a relative or schemeless value is refused
  (`cmd/a2a/main.go`, `checkAgentURL`). Missing `DATABASE_URL` is reported in the same message.
- **Fix**: set an absolute URL such as `http://<release>-a2a.<ns>.svc.cluster.local:8093/`. The
  chart derives it when `a2a.agentUrl` is empty.

### Boot hangs, then `retrying` WARN lines, then exit

- **Cause**: Postgres is unreachable. Migrations and the first ping retry 5 times with 1, 2, 4
  and 8 s backoff (`internal/bootretry`), then the process refuses to boot.
- **Check**: `retrying` lines with `op` = `run migrations` or `ping postgres` and the error.
- **Fix**: restore the database or the DSN. Behind PgBouncer in transaction mode, set
  `MIGRATIONS_DATABASE_URL` to a direct DSN: golang-migrate's session advisory lock does not
  survive pooling.

### Migration error mentioning `dirty`

- **Cause**: a previous migration died half-way and left `schema_migrations.dirty = true`.
- **Check**: `SELECT version, dirty FROM schema_migrations;`.
- **Fix**: repair the schema by hand to the state of the named version, then force that version
  with golang-migrate and restart. Do not delete `schema_migrations`.

## Readiness and traffic

### `/readyz` is 503 `dependency-unavailable` and the pod leaves the Service

- **Cause**: Postgres or Kafka failed its probe. The body names only the dependency
  (`not ready: postgres` or `not ready: kafka`), never the cause.
- **Check**: the WARN `readiness: dependency is not ready` has `dependency` and `error`.
  `/healthz` stays 200 and REST reads stay 200 while only Kafka is down.
- **Fix**: restore the dependency; recovery is automatic within the 3 s verdict cache.

### `/readyz` 503 `not_ready` during a rollout

- **Cause**: shutdown started: the drain gate is closed for `SHUTDOWN_DRAIN_DELAY` (5 s).
- **Fix**: none; it is intended. If clients see errors anyway, check that the chart's
  `terminationGracePeriodSeconds` is at least drain + 35 s.

### Curl answers `resource-not-found` or something unrelated

- **Cause**: another process owns the port (on a developer machine `localhost` may resolve to
  `[::1]` where a different server listens).
- **Fix**: call `127.0.0.1` explicitly, or check `lsof -i :8080`.

## REST answers

| Answer | Cause | Fix |
| --- | --- | --- |
| `404 not-found` on `GET /scorecards/{nodeId}` | No observation has ever been recorded for the node; a scorecard exists from the first observation. | Check `SELECT * FROM scorecards WHERE node_id = '...'` and whether the node's events reach the consumers. A node that only appears in `NodeRegistered` has no scorecard. |
| `200 {"items":[]}` for `grade=A` | No scorecard currently has that grade, or fewer than 50 observations everywhere (all `PROVISIONAL`). | Query `grade=PROVISIONAL`. |
| `422 validation-failed` `grade must be one of A, B, C, D, PROVISIONAL` | Bad `grade`. | Fix the query; `window` is `7d` or `30d`, `limit` 1 to 500. |
| `422 validation-failed` `limit must be between 1 and 500` | `limit=0` or above 500. | Omit `limit` for the default of 100. |
| `400 malformed-request-query` | A NUL character in the query string. | Remove it. |
| `405 method-not-allowed` | Anything but `GET`: the API has no write route. | Facts arrive only as CloudEvents on Kafka. |
| `409 concurrent-update` | A version-guarded save lost a race (the problem type exists for completeness; reads do not raise it). | Retry. |
| `500 internal-error` | A database error or a bug; the cause is in the ERROR log `request failed` with the same `request_id`. | Check Postgres. |

## Scorecards look wrong or stale

### A node has data but stays `PROVISIONAL` with no grade

- **Cause**: fewer than `MinObservations` = 50 observations inside the window
  ([ADR 0002](/docs/adr/0002-aggregates-and-invariants)). A `PROVISIONAL` scorecard publishes
  nothing.
- **Check**: `metrics.observations` in the read, or
  `SELECT count(*) FROM scorecard_observations WHERE scorecard_id LIKE '<nodeId>:%'`.
- **Fix**: none; the node has to produce more facts.

### Scorecards do not move although facts arrive

- **Cause 1**: the recompute scheduler fails every round. **Check**: ERROR
  `recompute round failed` and the counter `nodeperformance_scorecards_recomputed_total` with
  `outcome="failed"`; `reason="internal-error"` is usually Postgres.
- **Cause 2**: nothing is due. Only scorecards with an observation recorded since the last
  recompute, or one that left the window, are recomputed, and `NodeScorecardUpdated` is raised only
  when state, grade or metrics changed (never `PROVISIONAL` to `PROVISIONAL`).
- **Cause 3**: the consumers are not receiving (see below).
- **Fix**: remove the cause; the next tick logs `recompute round` with `recomputed > 0`. A restart
  forces an immediate round.

### `PackageHandedOver` or an acknowledgement never counts

- **Cause**: the purchase-order facts are not here yet. A handover needs `requiredShipBy` (from
  `PurchaseOrderIssued` or `VendorShipmentConfirmed`) and an acknowledgement needs `issuedAt` and
  `ackBy` (from `PurchaseOrderIssued`). The intake answers `purchase order not known yet`, retries
  5 times, then dead-letters ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-facts)).
- **Check**: `SELECT * FROM purchase_order_facts WHERE po_number = '...'` and the DLQ topic.
- **Fix**: make sure `vendor-connectivity` events for that purchase order were consumed, then
  republish the dead-lettered payload to the source topic.

### Inbound timeliness or accuracy is missing

- **Cause**: those two metrics exist only where a `InboundReceiptSubmitted` joins an
  `InboundShipmentShipped` of the same `shipmentId` inside the window (they are informational,
  never graded, [ADR 0009](/docs/adr/0009-inbound-timeliness-and-accuracy-metrics)). A receipt
  parked without its shipment is joined when the shipment arrives. They are served by REST only;
  MCP and A2A omit them.
- **Check**: `inbound_shipments` and `inbound_receipts` for the `shipment_id`.

## Consumers, DLQ and Kafka

### Messages produced to a topic are never applied

- **Cause**: the consumer group has no assignment. Seen in a real run: a service started while the
  topic did not yet hold messages kept an empty assignment, and an event produced afterwards was
  not consumed until the service was restarted. A wrong `KAFKA_BROKERS` or a group id typo shows
  the same way.
- **Check**: `kafka-consumer-groups --describe --group <group id>` (members, partitions, lag);
  the startup lines `consumer configured` name each topic.
- **Fix**: restart the pod; verify the group id matches the chart value.

### Pod restarts: worker stopped

- **Cause**: a worker returned on its own, so the process runs the shutdown sequence and exits
  non-zero (by design). For the consumers the usual reason is a refused connection to the broker:
  only timeouts, resets and EOFs are retried (up to 10 consecutive fetches); connection refused is
  fatal. Seen in a real run against a closed port: `worker stopped` ... `failed to dial: failed to
  open connection to localhost:29092: dial tcp [::1]:29092: connect: connection refused`, about 10 s
  after boot, then exit 1.
- **Check**: `kubectl logs deploy/node-performance --previous`; the `worker` field names the loop.
- **Fix**: restore the broker or `KAFKA_BROKERS`; the orchestrator restarts the pod (the restart
  alert `WarehousePodRestarting` fires at 2 restarts in 15 min).

### `handling failed ... retrying the same message`, lag growing

- **Cause**: a transient failure (database down, purchase-order facts not arrived). Backoff 200 ms
  doubling to 5 s, at most 5 attempts per message, then the message is dead-lettered and the
  consumer moves on.
- **Check**: ERROR lines with `attempt`/`max_attempts`; the topic `<topic>.dlq`.
- **Fix**: remove the cause. See [Runbook, dead-letter queues](/docs/operations/runbook#dead-letter-queues)
  to read `x-dlq-error` and redeliver.

### A consumed topic does not exist yet

- **Cause**: topics are auto-created by the first writer; a producer that has not written yet leaves
  the topic absent. This is normal for a not-yet-integrated sibling and never makes `/readyz` fail.
- **Check**: only fetch retries or an idle consumer in the logs.

## Outbox

### `NodeScorecardUpdated` is not seen downstream

- **Cause**: events are appended to `outbox_events` and published later by the relay.
- **Check**: `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` grows while Kafka is
  down and drains when it returns. ERROR `outbox relay pass failed` shows the send error.
- **Fix**: restore the broker. Nothing is lost; rows are published in id order.

### One row parked after `OUTBOX_MAX_ATTEMPTS`

- **Cause**: the row failed 20 times (default); the relay excludes it from claims and logs
  `outbox relay: row reached the attempt cap and is excluded from future claims` once, with
  `outbox_id`, `topic`, `event_type`, `key` and `last_error` in the row.
- **Fix**: read `last_error`, fix the cause, then
  `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. While it is parked, later events of the
  same scorecard can overtake it.

## MCP and A2A

| Symptom | Cause | Fix |
| --- | --- | --- |
| MCP calls are rejected before `tools/list` | The request lacks `Accept: application/json, text/event-stream`, or later calls omit the `Mcp-Session-Id` returned by `initialize`. | Use an MCP client; Streamable HTTP is the only transport (no stdio, no SSE). |
| A2A JSON-RPC error `-32009` `this version is not supported: requested "0.3", served "1.0"` | The call carries no `A2A-Version: 1.0` header. | Send `A2A-Version: 1.0` (the Agent Card says `protocolVersion` `1.0`). |
| A2A answer contains a `problem` with `unknown-skill` (400) | `skill` is not `node_performance` or `nodes_by_grade`. | See the Agent Card skills. |
| MCP tool result `isError: true` with `not-found: ...` or `validation-failed: ...` | The node has no scorecard / a bad `grade`, `window` or `limit`. | Same slugs as REST. |
| Agent Card advertises the wrong URL | `A2A_AGENT_URL` points at a name the callers cannot resolve. | Fix the variable or `a2a.agentUrl`. |
