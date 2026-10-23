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
- node-topology: node events, consumed without effect.

From the observations it keeps two `NodeScorecard`s per node, a 7-day and a 30-day one, and computes
`onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`, `promiseKeptRate` and `feedFreshnessP95Minutes`.
Below 50 observations a scorecard is `PROVISIONAL` and carries no grade; from 50 it is graded `A` to `D`.

It publishes `NodeScorecardUpdated` (integration topic `retailnetwork.node-performance.events`) and
`NodeThresholdBreached` (analytics only, `retailnetwork.node-performance.analytics`), and serves two reads:

| Route | Use case |
| --- | --- |
| `GET /scorecards/{nodeId}` | both scorecards of a node |
| `GET /scorecards?grade=&window=&limit=&after=` | scorecards by current grade, paginated |

Contracts: [`apis/openapi.yaml`](apis/openapi.yaml), [`apis/asyncapi.yaml`](apis/asyncapi.yaml).

## Status and design

The design is proposed in [retail-network-docs](https://iqvo.github.io/retail-network-docs/contexts/node-performance);
`model/model.yaml` there is the single source of truth. This repository is a service of the retail network, not of the
warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

Decisions are in [`docs/adr/`](docs/adr/index.md): the aggregate and its invariants with numbered assumptions (0002),
consumed contracts and local facts (0003), the CloudEvents catalogue (0004), the outbox relay (0005), the HTTP adapter,
consumers and readiness (0006). Configuration, probes and shutdown are in [`docs/runtime.md`](docs/runtime.md); the packaging
checklist is [`docs/before-first-release.md`](docs/before-first-release.md).

## Runtime

Go, hexagonal (`internal/domain`, `internal/application`, `internal/adapters`, `cmd/api`), real PostgreSQL and Kafka only
(`DATABASE_URL`, `KAFKA_BROKERS` and five `*_CONSUMER_GROUP` variables are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.partner.node-performance.scorecard.<Event>`), transactional outbox with an attempt cap, chi REST adapter
with problem+json, dependency-aware `/readyz`.

## Deployment

Helm chart: [`charts/node-performance/`](charts/node-performance/) (one component, the `api`). It refuses to render without
`database.url` or `database.existingSecret` (the DSN always reaches the pod through a Secret), `kafka.brokers`, the five
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
