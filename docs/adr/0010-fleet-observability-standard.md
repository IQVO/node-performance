# ADR 0010: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

node-performance logged JSON with `log/slog` and nothing else: no trace or span id on a log line, no
request id, no metrics, no traces, no `traceparent` on the events it publishes, and no OpenTelemetry
settings in the Helm chart. The other services of the fleet share one pipeline and one convention (the
"standard metrics convention" of inventory-storage ADR 0016 and wes-work-planning ADR 0013, adopted by
customer-ordering ADR 0007):

- pods export OTLP/gRPC to the in-cluster Collector
  (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger (traces) and a Prometheus
  exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a scorecard update could not be followed from the Kafka fact that moved it
through the recompute to the event it published, and no dashboard could show grading throughput or
errors. A service-specific setup was rejected (every repo should answer the same query the same way), as
were the OTel auto-instrumentation agents (they cannot follow a trace across `segmentio/kafka-go`, which
has no instrumentation, nor name spans by the chi route pattern).

This change is observability only: no business logic, REST contract, event payload or CloudEvents
attribute, database schema or authentication changes.

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup` (OTLP/gRPC traces and
   metrics, W3C TraceContext + Baggage propagator, Go runtime metrics; exporters dial lazily, so no
   Collector means dropped telemetry, never a blocked boot or request; returns a shutdown func) and
   `WithTraceContext` (stamps `trace_id`/`span_id` on a log record written with a span-carrying context).
   Only the composition roots (`cmd/api`, `cmd/mcp`) and the other adapters import it; `internal/domain`
   and `internal/application` never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`, `LOG_LEVEL`,
   uppercase levels. Code on a request or message path uses the `*Context` slog variants.
3. **HTTP middleware order** on both chi routers (REST and MCP): `RequestID` -> `otelchi.Middleware(
   serviceName, WithChiRoutes(r))` -> `otelchimetric.NewServerRequestDuration` -> `RequestLogger` ->
   recover; the existing `rejectNUL` stays after them. The REST router keeps its own panic recoverer (a
   panic is a 500 `problem+json`, ADR 0006) in the `Recoverer` slot instead of chi's. `telemetry.Setup`
   runs before the router is built (otelchimetric binds the global MeterProvider at construction) and is
   flushed last on shutdown. The request line carries `method`, `route` (the chi pattern, never the raw
   path), `status`, `duration_ms`, `bytes` and `request_id`; 5xx is ERROR, 4xx WARN, the rest INFO;
   `/healthz` and `/readyz` are not logged. `RequestLogger` lives in `internal/adapters/inbound/requestlog`,
   not in the REST adapter, because `cmd/mcp` may not import the REST adapter (ADR 0007's read-only
   fitness test) and both routers must log identically. It replaces the REST adapter's former `accessLog`
   (message `request`, fields `path`/`duration`).
4. **Traces over Kafka.** `OutboxRepo.Append` stamps `traceparent`/`tracestate` from the active span into
   the stored outbox headers, inside the transaction that writes the scorecard
   (`internal/adapters/outbound/postgres/outbox_trace.go`). The relay publishes the headers unchanged as
   Kafka headers; each of the five inbound consumers extracts them (a consumed message without them
   starts a new trace) and runs every handling attempt in a consumer span named `<topic> process`, so the
   use case's logs carry the publisher's trace id and an event enqueued while handling continues the
   trace. Only extra headers are added: the CloudEvents attributes and body, and the encoder's output,
   are unchanged (ADR 0004).
5. **Environment contract.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME` (defaults `node-performance`
   and `node-performance-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT` (default `telemetry.DefaultEndpoint`, a bare
   `host:port` or a URL), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and `LOG_LEVEL`.
6. **Chart.** `otel.enabled`, `otel.endpoint`, `otel.serviceName` render `OTEL_EXPORTER_OTLP_ENDPOINT` and
   `OTEL_SERVICE_NAME` (`<name>-mcp` for the mcp component); `SERVICE_VERSION` is the image tag, or the
   chart appVersion when empty, for the api and the mcp component (`mcp.serviceVersion` still overrides);
   the api also renders `ENVIRONMENT` from `environment`. `extraEnv` refuses all four names.
   `charts/node-performance/tests/render_test.sh` asserts it and scans `cmd/mcp` for the variables the
   binary reads. The frontend component is static nginx with no Go process, so it renders no OTEL env.
7. **Tier 2 business metric.** One counter, `nodeperformance.scorecards.recomputed`, unit `{scorecard}`,
   attribute `outcome` (`recomputed` | `failed`) and, on a failure, `reason` (`concurrent-update` | 
   `internal-error`). `ports.ScorecardMetrics` is the application port; the `Recompute` use case calls it
   once per scorecard it handled (a scorecard that vanished or was no longer due is not an attempt); the
   OTel implementation is in the telemetry adapter and is injected from `cmd/api` into `usecases.Deps`; a
   nil `Deps.Metrics` is a no-op. It follows the fleet convention: dotted lowercase name, one counter with
   an `outcome` attribute rather than two counters, UCUM unit, a description that says what a human
   should conclude when it moves.

### Why this metric (assumption O1)

The model leaves the choice open. Reading `apis/openapi.yaml` (two read-only operations) and ADR 0001/0002,
the context's one decision that is its reason to exist is grading a node: the `Recompute` command is where
a scorecard is graded, a `NodeScorecardUpdated` or `NodeThresholdBreached` event is raised and the outbox
row is written. The nine `Intake` methods only record observations and the REST adapter only reads, so
neither carries an outcome a human acts on. Counting at the recompute (not per published event) also
covers the PROVISIONAL scorecards, which publish nothing but whose stall is just as visible. A flat
`recomputed` count while nodes keep reporting means grading has stalled and published grades go stale; a
rising `concurrent-update` is contention that the next scheduler run retries; a rising `internal-error`
means the service or its database is failing. The `reason` slugs are the two failure classes the use case
distinguishes (`ports.ErrConcurrentUpdate` vs anything else), a closed set, so cardinality is bounded.

Not part of this ADR: dashboards and alert rules, which live in warehouse-infra and are generated per
deployed context from the instrument names above.

## Consequences

- A Kafka fact, its log lines, the recompute and the events it enqueued, and the consumers that handle
  them in other services share one trace id when the publisher's span was active at the outbox write;
  `trace_id` in Loki jumps to Jaeger. The scheduler's recompute has no inbound span, so the events it
  raises start their own trace.
- Request rate, errors and duration come from `http.server.request.duration` (otelchimetric) with no
  hand-rolled counter; grading health from `nodeperformance.scorecards.recomputed`.
- Every new log call on a request or message path must use a `*Context` variant, or its line carries no
  trace id.
- A consumer span is created per handling attempt, so a retried message shows as several spans under the
  same parent.
- `OutboxRepo.Insert`-style raw paths stamp nothing; only `Append`, the path every use case takes, does.
- The access log line changes shape (message `http request`, `route` instead of `path`, `duration_ms`
  instead of `duration`); nothing in the repo or the chart parses it.
