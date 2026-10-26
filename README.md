# node-performance

Retail network, bounded context **Node Performance** (Supporting, partner tier). It measures every node against what it declared and what was asked of it (acknowledgement latency, rejections, on-time handover against requiredShipBy, promise-kept rate and inventory feed freshness) over rolling windows, and grades the node.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## What it does

It consumes what the network observed about every node and grades it from those facts alone:

- vendor-connectivity: purchase orders issued, accepted, rejected, expired, shipment confirmed;
- shipping-and-tracking: packages handed over (against the order's `requiredShipBy`);
- delivery-promise: promises kept and broken (only a node-attributable cause counts against the node);
- offer-availability: inventory feeds applied (feed freshness);
- node-topology: node events, consumed without effect;
- vendor-connectivity `InboundReceiptSubmitted` and seller-inbound `InboundShipmentShipped`: joined by `shipmentId` into two informational inbound metrics.

From the observations it keeps two `NodeScorecard`s per node, a 7-day and a 30-day one, and computes
`onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`, `promiseKeptRate` and `feedFreshnessP95Minutes`; the scorecard read adds, when there is data, `inboundTimelinessP95Minutes`
(p95 of receipt minus shipped time) and `inboundAccuracyRate` (cartons received over cartons reported). Those two are shown, never graded.
Below 50 observations a scorecard is `PROVISIONAL` and carries no grade; from 50 it is graded `A` to `D`.

It publishes `NodeScorecardUpdated` (integration topic `retailnetwork.node-performance.events`) and
`NodeThresholdBreached` (analytics only, `retailnetwork.node-performance.analytics`), and serves two reads:

| Route | Use case |
| --- | --- |
| `GET /scorecards/{nodeId}` | both scorecards of a node |
| `GET /scorecards?grade=&window=&limit=&after=` | scorecards by current grade, paginated |

Contracts: [`apis/openapi.yaml`](apis/openapi.yaml), [`apis/asyncapi.yaml`](apis/asyncapi.yaml).

### MCP server (read-only)

`cmd/mcp` is a second, independent deployable (ADR 0007): the same two read use
cases exposed to AI agents over the official MCP Go SDK, Streamable HTTP on
`:8090` at `/mcp`, with an open `/healthz` and no auth (fleet decision). Two
tools, one per read: `get_scorecard(nodeId)` and
`list_scorecards_by_grade(grade, window?, limit?, after?)`; typed errors carry
the REST problem slug (`not-found`, `validation-failed`). It reads only: no
Kafka, no consumers, no scheduler, no outbox — `DATABASE_URL` (plus the
optional `MIGRATIONS_DATABASE_URL`) is its only dependency, and it runs the
idempotent boot migrations like `cmd/api`. In the chart it is behind
`mcp.enabled` (off by default), with its own ClusterIP Service and an optional
Gateway API route (`/api/mcp/node-performance` rewritten to `/mcp`).

### Web console remote

`web/` is the `node_performance_mfe` Module Federation remote (ADR 0008): a
small read-only UI the warehouse-console shell mounts under
`/node-performance/*` — the scorecards of one grade (window filter, keyset
paging) and a node lookup showing both windows with their metrics. The API
origin comes from the shell's runtime `/config.json`
(`window.__WAREHOUSE_CONFIG__.apiOrigin` → `/api/node-performance`), never
from the build. Dev/preview on `:4016`; served in-cluster by its own
nginx-unprivileged pod behind `frontend.enabled` (off by default) at
`/mfes/node-performance/`. `@warehouse/ui-kit` is a sibling checkout, so run
npm commands from `web/` with `warehouse-ui-kit` next to this repository.


## Status and design

The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance);
`model/model.yaml` there is the single source of truth. This repository is a service of the retail network, not of the
warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

Decisions are in [`docs/adr/`](docs/adr/index.md): the aggregate and its invariants with numbered assumptions (0002),
consumed contracts and local facts (0003), the CloudEvents catalogue (0004), the outbox relay (0005), the HTTP adapter,
consumers and readiness (0006), the read-only MCP server (0007), the web console remote (0008), the informational inbound timeliness and accuracy metrics (0009). Configuration, probes and shutdown are in [`docs/runtime.md`](docs/runtime.md); the packaging
checklist is [`docs/before-first-release.md`](docs/before-first-release.md).

## Runtime

Go, hexagonal (`internal/domain`, `internal/application`, `internal/adapters`, `cmd/api`), real PostgreSQL and Kafka only
(`DATABASE_URL`, `KAFKA_BROKERS` and six `*_CONSUMER_GROUP` variables are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.partner.node-performance.scorecard.<Event>`), transactional outbox with an attempt cap, chi REST adapter
with problem+json, dependency-aware `/readyz`.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0010](docs/adr/0010-fleet-observability-standard.md)): JSON logs on stdout carry
`trace_id`/`span_id` and a `request_id`; `cmd/api` and `cmd/mcp` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request
duration, Go runtime metrics and the business counter `nodeperformance.scorecards.recomputed` (`outcome`, `reason`); the trace continues across
Kafka in the `traceparent` header of each published event and is picked up by the consumers. The chart's `otel.*` values render the env.

## Deployment

Helm chart: [`charts/node-performance/`](charts/node-performance/): the `api` always, plus the optional `mcp` and
`frontend` components (both off by default). It refuses to render without
`database.url` or `database.existingSecret` (the DSN always reaches the pod through a Secret), `kafka.brokers`, the six
`config.*ConsumerGroup` ids (stable, service-prefixed, never the pod name) and a `terminationGracePeriodSeconds` of at least
`config.shutdownDrainDelay` + 35 s (40 s by default). Defaults to the image `ghcr.io/iqvo/node-performance:v<appVersion>`.

```
helm template np charts/node-performance -f charts/node-performance/ci/default-values.yaml
bash charts/node-performance/tests/render_test.sh
python3 charts/node-performance/tests/test_service_selectors.py
```

## Develop

```
make check          # format, vet, build, lint, unit + handler + BDD tests (the pre-push gate)
make coverage       # >= 90% over internal/domain and internal/application
make mutation       # gremlins, one run per internal/domain package
make arch-test      # fitness tests
make integration    # testcontainers Postgres and Kafka (needs Docker)
make vuln
```
