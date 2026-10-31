---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
description: "Every environment variable of the three Node Performance binaries (api, mcp, a2a): default, required or not, meaning."
---

# Configuration

Node Performance ships **three binaries**: `cmd/api` (REST, six Kafka consumers,
recompute scheduler, outbox relay), `cmd/mcp` (read-only MCP) and `cmd/a2a`
(read-only A2A agent). All of them are configured only through environment
variables: there is no config file and no flag. REST, MCP and A2A are
unauthenticated fleet-wide, so there is no token or key to configure.

The service runs on real Postgres (and, for `cmd/api`, real Kafka) only
([ADR 0006](/docs/adr/0006-http-adapter-consumers-and-readiness)). A missing
mandatory variable is a boot error that names it, raised before anything is
opened or bound. Test-only variables are not listed; the integration tests start
their own containers with testcontainers.

A variable read by several binaries has a row in each table, with that binary's
own default (the default of `OTEL_SERVICE_NAME` differs per binary).

## `cmd/api`

Source: `cmd/api/config.go` (`loadConfig`), `cmd/api/telemetry.go`
(`loadTelemetryEnv`), `internal/adapters/outbound/postgres/config.go`.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service's pool (10 connections, `statement_timeout = 5s`). Boot error `missing required environment variable: DATABASE_URL`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the embedded boot migrations, because golang-migrate's session advisory lock does not survive a transaction-pooling PgBouncer. Empty falls back to `DATABASE_URL`. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated `host:port` list (entries trimmed, blanks dropped). Used by the consumers, the outbox relay and the `/readyz` broker check. Missing or blank is the boot error `missing required environment variable: KAFKA_BROKERS (comma-separated host:port list)`. From the host the fleet's single broker is `localhost:9092`. |
| `VENDOR_CONNECTIVITY_CONSUMER_GROUP` | none | **yes** | Group id of the consumer of `retailnetwork.vendor-connectivity.events`. Never derived from the pod name: it is a chart value. |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | none | **yes** | Group id of the consumer of `retailnetwork.shipping-and-tracking.events`. |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | none | **yes** | Group id of the consumer of `retailnetwork.delivery-promise.events`. |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | none | **yes** | Group id of the consumer of `retailnetwork.offer-availability.events`. |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | none | **yes** | Group id of the consumer of `retailnetwork.node-topology.events`. |
| `SELLER_INBOUND_CONSUMER_GROUP` | none | **yes** | Group id of the consumer of `retailnetwork.seller-inbound.events` (the shipped instant of each inbound shipment, [ADR 0009](/docs/adr/0009-inbound-timeliness-and-accuracy-metrics)). |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server (also serves `/healthz` and `/readyz`). |
| `RECOMPUTE_INTERVAL` | `1m` | no | Tick of the recompute scheduler. A Go duration (`30s`) or plain seconds (`30`). Unparsable or non-positive values log a WARN and use the default. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Poll interval of the outbox relay. Same parsing and fallback as `RECOMPUTE_INTERVAL`. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which the relay stops claiming an outbox row. `0` = unlimited. Negative or unparsable values log a WARN and use `20`, so a typo never removes the cap ([ADR 0005](/docs/adr/0005-outbox-relay-and-attempt-cap)). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | After SIGINT/SIGTERM `/readyz` flips to 503 and the process waits this long before closing the listener. `0` disables the wait. Negative or unparsable values log a WARN and use `5s`. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive. Anything else falls back to `info`. Logs are JSON on stdout. |
| `OTEL_SERVICE_NAME` | `node-performance` | no | `service.name` of spans and metrics, and the Loki `app` label. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, bare `host:port` or a URL. A bare value is normalised to `http://host:port`. Export never blocks: an unreachable Collector costs telemetry, not availability. |
| `SERVICE_VERSION` | `dev` | no | `service.version` of every span and metric. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` of every span and metric (read by `telemetry.Setup`). |

Misconfiguration is refused first: `loadConfig` runs before telemetry, the
database or the listener, so a bad environment never half-starts the service.

## `cmd/mcp`

Source: `cmd/mcp/main.go` (`run`), `cmd/mcp/telemetry.go`. A read-only MCP server
over Streamable HTTP ([ADR 0007](/docs/adr/0007-mcp-server-adoption)). It reads
the service database and never dials Kafka, so it has no `KAFKA_BROKERS` and no
consumer group.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the database `cmd/api` owns. Missing is the boot error `DATABASE_URL is required: this context runs on real Postgres`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations (this binary also runs the idempotent embedded migrations; the advisory lock makes concurrent starts safe). |
| `LOG_LEVEL` | `info` | no | As for `cmd/api`. |
| `OTEL_SERVICE_NAME` | `node-performance-mcp` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version`, also logged at boot. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`, also logged at boot. |

## `cmd/a2a`

Source: `cmd/a2a/main.go` (`loadBootConfig`, `run`), `cmd/a2a/telemetry.go`. A
read-only Agent2Agent agent ([ADR 0011](/docs/adr/0011-a2a-inbound-adapter)).
Like `cmd/mcp` it never dials Kafka.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL of this agent's JSON-RPC endpoint, advertised on the Agent Card (for example `http://<release>-a2a.<ns>.svc.cluster.local:8093/`). Missing, relative or schemeless is a boot error naming it. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the database `cmd/api` owns. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `LOG_LEVEL` | `info` | no | As for `cmd/api`. |
| `OTEL_SERVICE_NAME` | `node-performance-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version`, also advertised on the Agent Card. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`, also logged at boot. |

`loadBootConfig` reports every mandatory problem at once (`DATABASE_URL` and
`A2A_AGENT_URL` together) before telemetry, any connection and the listener.

## Compiled-in settings (not configurable)

Constants in the source; changing them needs a code change.

| Setting | Value | Source |
| --- | --- | --- |
| HTTP `ReadHeaderTimeout` | 5 s (all three binaries) | `cmd/api/config.go`, `cmd/mcp/main.go`, `cmd/a2a/main.go` |
| HTTP graceful shutdown / per-worker drain timeout (`cmd/api`) | 10 s / 10 s | `cmd/api/config.go` (`shutdownTimeout`, `workerDrainTimeout`) |
| HTTP graceful shutdown (`cmd/mcp`, `cmd/a2a`) | 10 s | `shutdownTimeout` in each `main.go` |
| Postgres pool size and statement timeout | 10 connections, `statement_timeout = 5s` | `internal/adapters/outbound/postgres/pool.go` |
| Boot retry (migrations, first ping) | 5 attempts, backoff 1 s, 2 s, 4 s, 8 s | `internal/bootretry` |
| Telemetry flush on shutdown | 5 s | `telemetryFlushTimeout` in each `telemetry.go` |
| Metrics push interval | 15 s | `internal/adapters/outbound/telemetry/telemetry.go` (`metricInterval`) |
| `/readyz` verdict cache / check timeout | 3 s / 2 s | `internal/adapters/inbound/http/readiness.go` |
| Consumer handling attempts, backoff | 5 attempts, 200 ms doubling to 5 s, then `<topic>.dlq` | `internal/adapters/inbound/kafka/deadletter.go`, `kafka.go` |
| Minimum observations for a graded scorecard | 50 (below: `PROVISIONAL`, nothing published) | `internal/domain/scorecard` ([ADR 0002](/docs/adr/0002-aggregates-and-invariants)) |

## What is deliberately absent

- **No auth variables.** REST, MCP and A2A are unauthenticated; access control is
  the in-cluster ClusterIP boundary.
- **No in-memory or log-sink mode.** `cmd/**` never imports the memory adapter;
  there is no `*_MODE` variable (the chart refuses one in `extraEnv`).
- **No Kafka variable on `cmd/mcp` and `cmd/a2a`.** They can never publish.
- **Chart values.** `charts/node-performance` maps these variables onto the pod
  (`database.*`, `kafka.brokers`, `config.*`, `otel.*`); see the
  [Runbook](/docs/operations/runbook#deployment-helm). The chart deploys the
  `api` by default; `mcp` and `a2a` are opt-in.
