---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "Node Performance telemetry: OTel instruments, spans, log fields, exporter variables, and the dashboards and alerts that exist in warehouse-infra."
---

# Observability

The three binaries follow the fleet observability standard
([ADR 0010](/docs/adr/0010-fleet-observability-standard)): JSON logs on stdout, and
traces and metrics exported over OTLP/gRPC to the Collector. Export is non-blocking: the
exporters connect lazily, so a Collector that is down or absent costs telemetry, never
availability or start-up (`internal/adapters/outbound/telemetry/telemetry.go`).

## Exporter variables

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or URL. A bare value is rewritten to `http://host:port` (and the variable is rewritten in place so the SDK stops logging a parse error). |
| `OTEL_SERVICE_NAME` | `node-performance` (`api`), `node-performance-mcp`, `node-performance-a2a` | `service.name` of every span and metric, and the Loki `app` label. |
| `SERVICE_VERSION` | `dev` | `service.version`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. |
| `OTEL_RESOURCE_ATTRIBUTES` | none | Read by the SDK default resource (`telemetry.sdk.*` and extra attributes). |
| `LOG_LEVEL` | `info` | Log threshold; also gates the SDK's own diagnostics, which are bridged onto the slog handler and logged at DEBUG as `opentelemetry error`. |

Metrics are pushed every 15 s by a periodic reader. Propagation is W3C `traceparent` plus
baggage. The chart renders the first four from `otel.*`, `image.tag` and `environment`.

## Metrics

| Instrument | Type | Unit | Attributes | Source |
| --- | --- | --- | --- | --- |
| `nodeperformance.scorecards.recomputed` (Prometheus: `nodeperformance_scorecards_recomputed_total`) | Int64 counter | `{scorecard}` | `outcome` = `recomputed` \| `failed`; `reason` on failures = `concurrent-update` \| `internal-error` | `internal/adapters/outbound/telemetry/scorecard_metrics.go`, recorded by `Recompute.Run` (`internal/application/usecases/recompute.go`) in the `api` process only |
| `http.server.request.duration` | Float64 histogram | `s` | route pattern, method, status class (otelchi metric) | `otelchimetric.NewServerRequestDuration` in the REST, MCP and A2A routers |
| Go runtime metrics (goroutines, memory, GC) | gauges / counters | n/a | none | `go.opentelemetry.io/contrib/instrumentation/runtime` v0.72.0, started by `telemetry.Setup`; instrument names depend on that library's naming mode, the dashboard queries accept both `go_goroutine_count` and `runtime_go_goroutines` |

The counter's meaning (from its description in the code): a `recomputed` count that stays flat
while nodes keep reporting means grading has stalled and published grades are going stale; a
rising `concurrent-update` means writers contend for the same scorecards but the next run
retries them; a rising `internal-error` means the service itself, usually its database, is
failing. A scorecard that vanished or was no longer due is not counted as an attempt.

There is no consumer-lag, outbox-backlog or DLQ instrument. Use the SQL and log signals in the
[Runbook](/docs/operations/runbook#outbox-relay) for those.

## Spans

| Span | Kind | Where | Notes |
| --- | --- | --- | --- |
| `<METHOD> <route pattern>` (for example `GET /scorecards/{nodeId}`) | server | `otelchi` middleware in the `api`, `mcp` and `a2a` routers | named after the chi route pattern, never the raw path |
| `<topic> process` (for example `retailnetwork.delivery-promise.events process`) | consumer | `internal/adapters/inbound/kafka/tracing.go` | attributes `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`; parented to the `traceparent` of the message when present, otherwise a new trace; handler errors are recorded on the span |
| `a2a.skill <skill>` | internal | `internal/adapters/inbound/a2a/skills.go` | attribute `a2a.skill`; status Error with the problem detail when the skill answered a problem |

Trace continuity across Kafka: when a span is active while events are appended to the outbox
(an event applied by a consumer span), the `traceparent`/`tracestate` headers are stored with
the outbox row and published as ordinary Kafka headers
(`internal/adapters/outbound/postgres/outbox_trace.go`); the CloudEvents attributes and body
are untouched. Events appended by the recompute scheduler carry no span context.

## Logs

JSON on stdout, one object per line (`log/slog` JSON handler wrapped by
`telemetry.WithTraceContext`). Every record written with a span-carrying context also has
`trace_id` and `span_id`. Standard keys: `time`, `level`, `msg`.

Access log (REST, MCP and A2A routers, `internal/adapters/inbound/requestlog`), message
`http request`: `method`, `route` (the matched pattern, `unmatched` for anything else),
`status`, `duration_ms`, `bytes`, `request_id`. A 5xx is logged at ERROR, a 4xx at WARN,
everything else at INFO; `/healthz` and `/readyz` are not logged. Real line:

```json
{"time":"2026-10-31T19:21:24.175464-03:00","level":"WARN","msg":"http request","method":"GET","route":"/scorecards/{nodeId}","status":404,"duration_ms":6,"bytes":137,"request_id":"host/udMqaZi8zF-000002","trace_id":"9de15fcab788ae2cd9d3553a8e37d00c","span_id":"e6033835721cfa63"}
```

Lifecycle and worker messages worth alerting or grepping on:

| Message | Level | Fields | Meaning |
| --- | --- | --- | --- |
| `telemetry configured` | INFO | `service_name`, `otlp_endpoint` | exporters created |
| `postgres adapters configured` / `postgres reader configured` | INFO | | migrations ran and the pool answered a ping |
| `consumer configured` | INFO | `consumer`, `topic` | one per consumer, at boot |
| `worker running` / `worker stopped` | INFO / ERROR | `worker`, `error` | a stopped worker makes the process exit non-zero |
| `outbox relay configured` | INFO | `interval`, `max_attempts`, `events_topic`, `analytics_topic` | relay settings in force |
| `http server listening` | INFO | `addr` | |
| `recompute round` / `recompute round failed` | INFO / ERROR | `recomputed`, `events`, `conflicts` / `error` | one line per scheduler tick |
| `event processed` | INFO | `type`, `event_id`, `subject`, `outcome` (`applied`, `duplicate`, `ignored`) | one per consumed event |
| `skipping a message that is not a valid CloudEvent` | WARN | `consumer`, `topic`, `error` | skipped, not dead-lettered |
| `dead-lettering an event that can never be applied` | WARN | `consumer`, `type`, `event_id`, `error` | invalid payload, sent to `<topic>.dlq` at once |
| `<consumer> handling failed; retrying the same message` | ERROR | `error`, `partition`, `offset` | transient failure, retry with backoff |
| `<consumer> handling failed after the maximum attempts; dead-lettering the message` | ERROR | `error`, `partition`, `offset`, `attempts` | 5 attempts exhausted |
| `<consumer> broker connection stalled or lost; fetching again after the backoff` | WARN | | broker unreachable |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | ERROR | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` | once per parked row |
| `outbox relay pass failed` | ERROR | `error` | the pass is retried after the interval |
| `readiness: dependency is not ready` | WARN | `dependency` (`postgres` or `kafka`), `error` | the cause behind a `/readyz` 503 |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | INFO | `drain_delay` | shutdown started |
| `worker did not stop before the shutdown drain deadline` | WARN | `worker` | a worker overran its 10 s bound |
| `retrying` / `succeeded after retry` | WARN / INFO | `op`, `attempt`, `in`, `err` | boot retries of migrations and the first ping |
| `mcp tool failed with an unexpected error`, `a2a skill failed` | ERROR | `error` | an unexpected (non-problem) failure in the MCP or A2A adapter |
| `panic in handler`, `request failed` | ERROR | `panic`/`error`, `method`, `path` | a REST 500 |

## Dashboards and alerts that exist

Only what is in warehouse-infra `develop`; none of it is defined in this repository.

- **Dashboard** `warehouse-node-performance` (`terraform/dashboards/contexts/node-performance.json`):
  Kong request rate, 5xx ratio and p95 request versus upstream latency for the context's routes;
  request rate and p95 duration by `service_name=~"node-performance.*"`; the business counter
  `sum by (outcome) (rate(nodeperformance_scorecards_recomputed_total[5m]))`; Go goroutines and
  memory; Loki live stream, log volume by level and errors-and-warnings for
  `{app="node-performance", container!~"istio-proxy|istio-init"}`.
- **Alerts** (`terraform/alerting/warehouse-alert-rules.json`): `WarehouseKong5xxRatioHigh`,
  `WarehousePodRestarting`, and the SLO burn alerts `WarehouseApiAvailabilityBurnFast`,
  `WarehouseApiLatencyBurnFast` (critical) and `...BurnSlow` (warning).
- **Not covered**: no alert on `outcome="failed"`, consumer lag, DLQ traffic or the unpublished
  outbox tail. They are manual checks (see [Troubleshooting](/docs/operations/troubleshooting)).
