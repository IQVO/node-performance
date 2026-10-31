# Node Performance

[![CI](https://github.com/IQVO/node-performance/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/node-performance/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/node-performance/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/node-performance/blob/develop/go.mod) [![release](https://img.shields.io/github/v/release/IQVO/node-performance?sort=semver)](https://github.com/IQVO/node-performance/releases) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/node-performance/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/node-performance/blob/develop/LICENSE)

Retail network, bounded context **Node Performance** (Supporting Subdomain,
partner tier). It measures every node against what it declared and what was
asked of it (acknowledgement latency, rejections, on-time handover against
`requiredShipBy`, promise-kept rate and inventory feed freshness) over rolling
windows, and grades the node. It owns only the `NodeScorecard` aggregates and
the observations behind them — never orders, promises, inventory or topology —
and it grades from observed facts alone. Its downstream consumers
(`delivery-promise` and `order-sourcing`, both Conformists to the scorecard
event) read the grades; this service is a service of the retail network, not of
the warehouse-systems fleet, and the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

The context is documented on the fleet-wide warehouse-docs site, in the
retail-network section: **<https://iqvo.github.io/warehouse-docs/network/contexts/node-performance>**
(the model under `network/model/model.yaml` in IQVO/warehouse-docs is the
single source of truth). Fleet-wide network pages:
[strategic design](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification),
[the CloudEvents event standard](https://iqvo.github.io/warehouse-docs/network/strategic-design/event-standard-cloudevents),
the [node model](https://iqvo.github.io/warehouse-docs/network/network-model/node-model),
and the [WMS/WES integration seam](https://iqvo.github.io/warehouse-docs/network/integration/wms-wes-integration).
This repository's own references are [`docs/runtime.md`](docs/runtime.md)
(configuration, probes, shutdown), [`docs/operations/runbook.md`](docs/operations/runbook.md)
and the packaging checklist [`docs/before-first-release.md`](docs/before-first-release.md).

Page map (on the warehouse-docs site, under `network/contexts/node-performance/`):

| Area | Pages |
|---|---|
| Business context | [Business context](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/business-context), [Use cases](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/use-cases) |
| Language & model | [Ubiquitous language](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/ubiquitous-language), [Domain events](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/domain-events), [Domain message flow](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/domain-message-flow), [Class diagram](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/class-diagram), [Sequence diagrams](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/sequence-diagrams), [Entity-relationship](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/entity-relationship) |
| DDD canvases | [Core domain chart](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/bounded-context-canvas), [Aggregate design canvas](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/aggregate-design-canvas), [EventStorming](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/eventstorming), [Context map](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/context-map) |

## What it does

It consumes what the network observed about every node and grades it from
those facts alone:

- vendor-connectivity: vendor onboarding, purchase orders issued, accepted,
  rejected, expired, shipment confirmed, submission outcomes, inbound receipts
  submitted;
- shipping-and-tracking: packages handed over (against the order's
  `requiredShipBy`);
- delivery-promise: promises kept and broken (only a node-attributable cause
  counts against the node);
- offer-availability: inventory feeds applied (feed freshness);
- node-topology: node registration and operational-profile events, consumed
  without grading effect;
- vendor-connectivity `InboundReceiptSubmitted` and seller-inbound
  `InboundShipmentShipped`: joined by `shipmentId` into two informational
  inbound metrics.

From the observations it keeps two `NodeScorecard`s per node, a 7-day and a
30-day one, and computes `onTimeHandoverRate`, `lateAckRate`, `rejectRate`,
`expiryRate`, `promiseKeptRate` and `feedFreshnessP95Minutes`; the scorecard
read adds, when there is data, `inboundTimelinessP95Minutes` (p95 of receipt
minus shipped time) and `inboundAccuracyRate` (cartons received over cartons
reported). Those two are shown, never graded. Below 50 observations a
scorecard is `PROVISIONAL` and carries no grade; from 50 it is graded `A` to
`D`.

## Architecture

Hexagonal / ports & adapters; dependencies point inward only:

```
cmd/api/                      main.go, run.go, workers.go, config.go — composition root (REST, six Kafka consumers, the recompute scheduler and the outbox relay in ONE process)
cmd/mcp/                      read-only MCP server (Streamable HTTP, ADR 0007)
cmd/a2a/                      read-only A2A agent (JSON-RPC over HTTP, ADR 0011)
internal/
  domain/                     pure business logic — scorecard (the NodeScorecard aggregate) and shared
  application/
    ports/                    OUT interfaces only
    repository/               read-side repository interfaces
    usecases/                 one struct per use case (recompute, reads, intake)
    views/                    the read models the API and MCP serve
    outbox/                   append-to-outbox use case
  adapters/
    inbound/http/             chi handlers, DTOs, problem+json mapping, readiness
    inbound/kafka/            the six consumers
    inbound/mcp/              MCP tools (read-only)
    inbound/a2a/              A2A skills (read-only)
    inbound/requestlog/       request logging middleware
    kafka/cloudevents/        CloudEvents 1.0 codec shared in/out
    outbound/postgres/        pgxpool repos, embedded migrations, pgtx UnitOfWork
    outbound/outbox/          transactional outbox + relay with attempt cap
    outbound/kafka/           integration and analytics publishers
    outbound/memory/          test doubles only, never linked into a binary
    outbound/clock/, telemetry/, repocontract/
  architecture/               the fitness tests
  bootretry/                  bounded boot retry
```

The layering is enforced as an executable test, not a convention:
`internal/architecture/architecture_test.go` (`make arch-test`) fails the
build if the domain ever imports an outer layer or an adapter reaches sideways
into its sibling.

## Integration

Everything on the wire is a **CloudEvents 1.0** event in structured content
mode (Kafka header `content-type: application/cloudevents+json;
charset=UTF-8`); `source` is `/retailnetwork/node-performance`, `subject` is
the scorecardId (`nodeId:window`), `dataschema` is
`urn:retailnetwork:node-performance:<stream>:<EventName>:v1`, and the Kafka
key is the subject. Published events go through the **transactional outbox**
(the row and its already-encoded event are written in one transaction;
ADR 0005).

**Published** (ADR 0004):

| Event | Topic | CloudEvents `type` |
|---|---|---|
| `NodeScorecardUpdated` | `retailnetwork.node-performance.events` | `com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated` |
| `NodeThresholdBreached` | `retailnetwork.node-performance.analytics` (analytics only, no in-organization consumer) | `com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached` |

`delivery-promise` and `order-sourcing` consume `NodeScorecardUpdated`; only
the 30-day window feeds their decisions, the 7-day window feeds alerts only.

**Consumed** — 15 events from six producers, each topic under its own stable
consumer group named by a mandatory environment variable (ADR 0003); every
message is deduplicated on the CloudEvents `id` in the `processed_events`
inbox in the same transaction as its effect, and unknown types are ignored:

| Topic | Events |
|---|---|
| `retailnetwork.vendor-connectivity.events` | `VendorOnboarded`, `PurchaseOrderIssued`, `PurchaseOrderAccepted`, `PurchaseOrderRejected`, `PurchaseOrderExpired`, `VendorShipmentConfirmed`, `SubmissionProcessed`, `InboundReceiptSubmitted` |
| `retailnetwork.shipping-and-tracking.events` | `PackageHandedOver` |
| `retailnetwork.delivery-promise.events` | `PromiseKept`, `PromiseBroken` |
| `retailnetwork.offer-availability.events` | `InventoryFeedApplied` |
| `retailnetwork.node-topology.events` | `NodeRegistered`, `NodeOperationalProfileDeclared` |
| `retailnetwork.seller-inbound.events` | `InboundShipmentShipped` (ADR 0009, the shipped instant of each inbound shipment) |

Full contracts: [`apis/asyncapi.yaml`](apis/asyncapi.yaml).

## Running it

Real PostgreSQL and real Kafka are the only runtime — there is no in-memory
mode and no log sink, and no compose file in this repository; point the binary
at your own broker and database (`make integration` and the CI `integration`
job boot their own Postgres/Kafka via testcontainers):

```sh
export DATABASE_URL="postgres://user:pass@localhost:5432/node_performance?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export VENDOR_CONNECTIVITY_CONSUMER_GROUP="node-performance.vendor"
export SHIPPING_AND_TRACKING_CONSUMER_GROUP="node-performance.shipping"
export DELIVERY_PROMISE_CONSUMER_GROUP="node-performance.promise"
export OFFER_AVAILABILITY_CONSUMER_GROUP="node-performance.offer"
export NODE_TOPOLOGY_CONSUMER_GROUP="node-performance.topology"
export SELLER_INBOUND_CONSUMER_GROUP="node-performance.seller-inbound"
go run ./cmd/api        # migrations run automatically at boot; :8080
```

A missing mandatory variable is a boot error that names it, raised before
anything is opened or bound. `cmd/mcp` and `cmd/a2a` need only `DATABASE_URL`
(plus their own address variables) — they never dial Kafka.

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | **required** | Postgres DSN of the service's pool |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy) |
| `KAFKA_BROKERS` | **required** | Comma-separated `host:port` list (consumers, relay and `/readyz`) |
| `VENDOR_CONNECTIVITY_CONSUMER_GROUP` | **required** | Stable group id on `retailnetwork.vendor-connectivity.events` |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | **required** | Group on `retailnetwork.shipping-and-tracking.events` |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | **required** | Group on `retailnetwork.delivery-promise.events` |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | **required** | Group on `retailnetwork.offer-availability.events` |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | **required** | Group on `retailnetwork.node-topology.events` |
| `SELLER_INBOUND_CONSUMER_GROUP` | **required** | Group on `retailnetwork.seller-inbound.events` (ADR 0009) |
| `HTTP_ADDR` | `:8080` | Listen address |
| `RECOMPUTE_INTERVAL` | `1m` | How often the scheduler recomputes the due scorecards |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited (ADR 0005) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `node-performance` | `service.name` of spans and metrics (ADR 0010) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC; export never blocks |
| `SERVICE_VERSION` / `ENVIRONMENT` | `dev` / `local` | `service.version` / `deployment.environment.name` |

`cmd/mcp` adds `MCP_ADDR` (`:8090`) and defaults `OTEL_SERVICE_NAME` to
`node-performance-mcp`; `cmd/a2a` adds `A2A_ADDR` (`:8093`) and the mandatory
`A2A_AGENT_URL`, and defaults `OTEL_SERVICE_NAME` to
`node-performance-a2a`. Both read `DATABASE_URL` (required),
`MIGRATIONS_DATABASE_URL`, `LOG_LEVEL`, `OTEL_EXPORTER_OTLP_ENDPOINT`,
`SERVICE_VERSION` and `ENVIRONMENT`.

## Observability

Logs, metrics and traces follow the fleet standard
([ADR 0010](docs/adr/0010-fleet-observability-standard.md)): JSON logs on
stdout carry `trace_id`/`span_id` and a `request_id`; `cmd/api`, `cmd/mcp` and
`cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so
a down Collector costs telemetry, nothing else) with HTTP request duration, Go
runtime metrics and the business counter
`nodeperformance.scorecards.recomputed` (`outcome`, `reason`); the trace
continues across Kafka in the `traceparent` header of each published event and
is picked up by the consumers. The chart's `otel.*` values render the env.

## API

Two read-only routes served by `cmd/api` on `:8080`:

| Route | Use case |
| --- | --- |
| `GET /scorecards/{nodeId}` | both scorecards of a node |
| `GET /scorecards?grade=&window=&limit=&after=` | scorecards by current grade, paginated (`limit` 1–500) |

Both routes are read-only, so none takes an `Idempotency-Key`. Errors are RFC
9457 problem details (`application/problem+json`): `not-found` (404) and
`validation-failed` (422). Contract: [`apis/openapi.yaml`](apis/openapi.yaml).

### MCP server (read-only)

`cmd/mcp` is a second, independent deployable (ADR 0007): the same two read
use cases exposed to AI agents over the official MCP Go SDK, Streamable HTTP
on `:8090` at `/mcp`, with an open `/healthz` and no auth (fleet decision).
Two tools, one per read: `get_scorecard(nodeId)` and
`list_scorecards_by_grade(grade, window?, limit?, after?)`; typed errors carry
the REST problem slug (`not-found`, `validation-failed`). It reads only: no
Kafka, no consumers, no scheduler, no outbox — `DATABASE_URL` (plus the
optional `MIGRATIONS_DATABASE_URL`) is its only dependency, and it runs the
idempotent boot migrations like `cmd/api`.

### A2A agent (read-only)

`cmd/a2a` is a third, independent deployable (ADR 0011): an Agent2Agent (A2A)
message-only agent over `github.com/a2aproject/a2a-go/v2`, JSON-RPC at `/` and
`/a2a` on `:8093` (`A2A_ADDR`), the Agent Card at
`/.well-known/agent-card.json`, an open `/healthz`, no auth (fleet decision).
`A2A_AGENT_URL` (the absolute URL the card advertises) is mandatory. Two
goal-shaped read skills over the same use cases as REST and MCP:
`node_performance` (`{nodeId}`) and `nodes_by_grade`
(`{grade, window?, limit?, after?}`); input is `{"skill","arguments"}` in the
first message part, decoded strictly, and rejections are answered in-band as
RFC 7807-shaped problems with the REST classes (404/422/500, plus 400 for a
malformed request or unknown skill). Like `cmd/mcp` it reads only
(`DATABASE_URL`, optional `MIGRATIONS_DATABASE_URL`): no Kafka, consumers,
scheduler or outbox.

## Helm chart

[`charts/node-performance/`](charts/node-performance/): the `api` always, plus
the optional `mcp`, `a2a` and `frontend` components (all off by default). It
refuses to render without `database.url` or `database.existingSecret` (the DSN
always reaches the pod through a Secret), `kafka.brokers`, the six
`config.*ConsumerGroup` ids (stable, service-prefixed, never the pod name) and
a `terminationGracePeriodSeconds` of at least `config.shutdownDrainDelay` +
35 s (40 s by default). Defaults to the image
`ghcr.io/iqvo/node-performance:v<appVersion>`.

```
helm template np charts/node-performance -f charts/node-performance/ci/default-values.yaml
bash charts/node-performance/tests/render_test.sh
python3 charts/node-performance/tests/test_service_selectors.py
```

`helm-lint` (pull requests) runs `ct lint` and the selector test; `chart-tests`
runs the render test; the `release` job packages
`node-performance-<version>.tgz`.

### Running the MCP server in Kubernetes

In the chart the MCP server is behind `mcp.enabled` (off by default), with its
own ClusterIP Service and an optional Gateway API route
(`/api/mcp/node-performance` rewritten to `/mcp`); the A2A agent is behind
`a2a.enabled` (off by default, route `/api/a2/node-performance` rewritten to
`/`). Both are read-only and unauthenticated by fleet decision — the
in-cluster ClusterIP boundary is the access control.

## Operator micro-frontend (web/)

`web/` is the `node_performance_mfe` Module Federation remote (ADR 0008): a
small read-only UI the warehouse-console shell mounts under
`/node-performance/*` — the scorecards of one grade (window filter, keyset
paging) and a node lookup showing both windows with their metrics. The API
origin comes from the shell's runtime `/config.json`
(`window.__WAREHOUSE_CONFIG__.apiOrigin` → `/api/node-performance`), never
from the build. Dev/preview on `:4016` (`npm run dev` in `web/`), served
in-cluster by its own nginx-unprivileged pod behind `frontend.enabled` (off by
default) at `/mfes/node-performance/`. `@warehouse/ui-kit` is a sibling
checkout, so run npm commands from `web/` with `warehouse-ui-kit` next to this
repository. `npm run test` (vitest), `npm run lint` (oxlint), `npm run
typecheck` (tsc).

## Local development / quality gate

```
make check          # format, vet, build, lint, unit + handler + BDD tests (the pre-push gate)
make check-fast     # fmt-check, vet, arch-test + changed-package tests (Stop hook)
make coverage       # >= 90% over internal/domain and internal/application
make mutation       # gremlins, one run per internal/domain package
make arch-test      # fitness tests
make integration    # testcontainers Postgres and Kafka (needs Docker)
make vuln           # govulncheck
```

lefthook (`lefthook install` once per clone) runs `fmt-check`, `vet`, `lint`
on pre-commit and `make check` on pre-push.

## Tests

- unit + handler tests: `make test` (`go test ./... -race`, CI job `test`,
  with the 90 % coverage gate in the same job)
- BDD acceptance: godog/Gherkin in `features/` (`make bdd`, CI job `bdd`)
- integration: testcontainers Postgres + Kafka (`make integration`, CI job
  `integration`; needs only Docker)
- mutation: gremlins per `internal/domain` package (CI job `mutation-fast`;
  the exhaustive `mutation` job is scheduled)
- architecture fitness: `internal/architecture` (CI job `arch-test`)
- API contract lint (CI job `api-lint`), spec/model drift (`drift`, scheduled),
  `lint`, `guide-lint`, `complexity`, `vuln`, `web` (the vitest/oxlint/tsc
  suite of `web/`), `helm-lint` + `chart-tests` (chart), `docker-publish` +
  `release` (main only)

## Architecture Decision Records

Index: [`docs/adr/index.md`](docs/adr/index.md) — 11 ADRs (0001–0011): the
bounded context (0001), aggregates and invariants (0002), consumed contracts
and local facts (0003), the CloudEvents catalogue (0004), the outbox relay
(0005), the HTTP adapter, consumers and readiness (0006), the read-only MCP
server (0007), the web console remote (0008), the inbound timeliness and
accuracy metrics (0009), the fleet observability standard (0010), the
read-only A2A agent (0011).

## License

[MIT](LICENSE).
