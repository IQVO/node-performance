---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Build Node Performance, run it on Postgres and Kafka, feed it one event and read the scorecard over REST, MCP and A2A."
---

# Quickstart

Build the service, see what it refuses to boot without, run it on Postgres and
Kafka, feed it one event and read the scorecard that results over REST, MCP and
A2A. Every output below was captured from a real run of the binaries built from
this branch against a throwaway Postgres 16 and a throwaway single-node Kafka.

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | `1.27.3` (`go.mod`) | everything |
| `make` | any | the targets below |
| Docker | any recent | the Postgres and Kafka containers below and `make integration` (testcontainers) |
| golangci-lint | `v2.14.0` (`GOLANGCI_VERSION` in the `Makefile`, pinned in CI) | `make lint`, `make check` |
| gremlins | `v0.6.0` (`GREMLINS_VERSION`) | `make mutation`, `make mutation-full` |
| govulncheck | latest | `make vuln` |
| Node 20 | | building this documentation site (`docs/`) and the operator micro-frontend (`web/`) |
| lefthook | optional | `lefthook install` wires `fmt-check`, `vet`, `lint` at pre-commit and `make check` at pre-push |

The fleet runs **one** Kafka broker (in-cluster, reachable from the host at
`localhost:9092` through the kind cluster); the docker-compose Kafka is retired
and this repository has no compose file. The service needs Postgres **and**
Kafka to boot and to be ready: there is no in-memory mode
([ADR 0006](/docs/adr/0006-http-adapter-consumers-and-readiness)).

## Build and test

```bash
make help          # list the targets
make build         # go build ./...
make test          # go test ./... -race (unit + handler + godog; no database needed)
make arch-test     # go test ./internal/architecture/... -v
make bdd           # go test ./... -run TestFeatures -v
make coverage      # tests with -race plus the 90% gate over domain + application
make check         # fmt-check vet build lint test
make check-all     # check + coverage + arch-test + bdd
make integration   # go test -tags=integration ./... -race -count=1 (needs Docker)
make vuln          # govulncheck
```

## Boot with nothing configured

```bash
go build -o node-performance ./cmd/api
./node-performance
```

It refuses to boot before it opens a connection or binds a port, and names the
first missing mandatory variable (exit status 1; real output):

```text
{"time":"2026-10-31T19:18:26.003889-03:00","level":"ERROR","msg":"service exited with error","error":"missing required environment variable: DATABASE_URL"}
```

Set `DATABASE_URL` and the next boot error names `KAFKA_BROKERS`, then each of
the six `*_CONSUMER_GROUP` variables in turn (see
[Configuration](/docs/operations/configuration)).

## Run on Postgres and Kafka

Start a throwaway Postgres (use the fleet broker at `localhost:9092`, or any
Kafka you own, for `KAFKA_BROKERS`; the run below used its own single-node
broker on `localhost:29092` so that nothing it published could reach the shared
platform topics):

```bash
docker run -d --rm --name np-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=np \
  -p 127.0.0.1:55434:5432 postgres:16-alpine

export DATABASE_URL='postgres://postgres:pw@127.0.0.1:55434/np?sslmode=disable'
export KAFKA_BROKERS=localhost:9092
export VENDOR_CONNECTIVITY_CONSUMER_GROUP=node-performance.vendor-dev
export SHIPPING_AND_TRACKING_CONSUMER_GROUP=node-performance.shipping-dev
export DELIVERY_PROMISE_CONSUMER_GROUP=node-performance.promise-dev
export OFFER_AVAILABILITY_CONSUMER_GROUP=node-performance.offer-dev
export NODE_TOPOLOGY_CONSUMER_GROUP=node-performance.topology-dev
export SELLER_INBOUND_CONSUMER_GROUP=node-performance.seller-inbound-dev
export HTTP_ADDR=127.0.0.1:18080
export RECOMPUTE_INTERVAL=5s        # default 1m; shortened to see a result quickly
./node-performance
```

The group ids are yours to choose (the code has none). Migrations run at boot
(`schema_migrations` plus the eight tables `idempotency_keys`,
`inbound_receipts`, `inbound_shipments`, `outbox_events`, `processed_events`,
`purchase_order_facts`, `scorecard_observations` and `scorecards`). The startup
log is JSON on stdout: `telemetry configured`, `postgres adapters configured`,
one `consumer configured` per consumer (`topic`), `worker running` for the six
consumers, the `recompute scheduler` and the `outbox relay`,
`outbox relay configured` (`interval`, `max_attempts`, `events_topic`,
`analytics_topic`) and `http server listening`. With no Collector on
`localhost:4317` nothing else happens: export never blocks.

Use `127.0.0.1` rather than `localhost` in the curl calls: another local
process may own `[::1]:18080`.

## First calls

Probes first, then a node nothing was observed about:

```bash
curl -si 127.0.0.1:18080/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

```bash
curl -si 127.0.0.1:18080/scorecards/N-0042
```

```text
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{"type":"not-found","title":"Not found","status":404,"detail":"not found: no scorecard for node N-0042","instance":"/scorecards/N-0042"}
```

Other observed answers (all `application/problem+json`):

| Request | Answer |
| --- | --- |
| `GET /scorecards?grade=A` on an empty database | `200 {"items":[]}` |
| `GET /scorecards?grade=Z` | `422` `validation-failed`, detail `validation failed: grade must be one of A, B, C, D, PROVISIONAL` |
| `GET /scorecards?grade=A&limit=0` | `422` `validation-failed`, detail `validation failed: limit must be between 1 and 500` |
| `POST /scorecards/N-0042` | `405` `method-not-allowed`, detail `POST is not allowed on /scorecards/N-0042` |

### Feed it one fact

Facts arrive only as CloudEvents on Kafka. Produce one `PromiseKept` event to
the delivery-promise topic (the console producer ships in the Kafka image;
`PromiseKept` needs no earlier purchase-order fact, unlike `PackageHandedOver`):

```json
{"specversion":"1.0","id":"3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c18","source":"/retailnetwork/delivery-promise","type":"com.retailnetwork.commerce.delivery-promise.promise.PromiseKept","subject":"prm-7f3a21","time":"2026-10-30T12:00:05Z","datacontenttype":"application/json","dataschema":"urn:retailnetwork:delivery-promise:events:PromiseKept:v1","data":{"promiseId":"prm-7f3a21","orderId":"ord-55a901","nodeId":"node-0f3a91","deliveredAt":"2026-10-30T12:00:00Z","deliverByLatest":"2026-10-30T13:00:00Z"}}
```

Send that line to `retailnetwork.delivery-promise.events` (for example
`kafka-console-producer --bootstrap-server <broker> --topic
retailnetwork.delivery-promise.events`). The consumer applies it, the next
recompute tick grades the node, and:

```bash
curl -si 127.0.0.1:18080/scorecards/node-0f3a91
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"nodeId":"node-0f3a91","scorecards":[{"scorecardId":"node-0f3a91:7d","nodeId":"node-0f3a91","window":"7d","state":"PROVISIONAL","grade":"PROVISIONAL","metrics":{"onTimeHandoverRate":1,"lateAckRate":0,"rejectRate":0,"expiryRate":0,"promiseKeptRate":1,"feedFreshnessP95Minutes":0,"observations":1},"computedAt":"2026-10-31T22:25:08Z"},{"scorecardId":"node-0f3a91:30d","nodeId":"node-0f3a91","window":"30d","state":"PROVISIONAL","grade":"PROVISIONAL","metrics":{"onTimeHandoverRate":1,"lateAckRate":0,"rejectRate":0,"expiryRate":0,"promiseKeptRate":1,"feedFreshnessP95Minutes":0,"observations":1},"computedAt":"2026-10-31T22:25:08Z"}]}
```

One observation is below the minimum of 50, so both scorecards are
`PROVISIONAL` (no grade, nothing published: `outbox_events` stays empty).
Listing by grade:

```bash
curl -s "127.0.0.1:18080/scorecards?grade=PROVISIONAL&window=30d&limit=10"
```

```json
{"items":[{"scorecardId":"node-0f3a91:30d","nodeId":"node-0f3a91","window":"30d","state":"PROVISIONAL","grade":"PROVISIONAL","metrics":{"onTimeHandoverRate":1,"lateAckRate":0,"rejectRate":0,"expiryRate":0,"promiseKeptRate":1,"feedFreshnessP95Minutes":0,"observations":1},"computedAt":"2026-10-31T22:25:08Z"}]}
```

:::note A consumer that joined before the event existed
In the captured run the first event was produced after the service had started
on a broker that had never held the topic; the group stayed empty and the event
was not applied until the service was restarted with the event already in the
topic. In the fleet the producers write continuously, so this only shows on a
fresh local broker: produce first, or restart the service. A consumed topic with
no messages shows no error, only an idle consumer.
:::

### MCP and A2A

Both are separate, read-only binaries over the same database and need only
`DATABASE_URL` (plus `A2A_AGENT_URL` for the agent):

```bash
DATABASE_URL=... MCP_ADDR=127.0.0.1:18090 go run ./cmd/mcp
DATABASE_URL=... A2A_ADDR=127.0.0.1:18093 A2A_AGENT_URL=http://127.0.0.1:18093/ go run ./cmd/a2a
```

The MCP tool list, input fields and annotations are on
[MCP tools](/docs/mcp/tools); the A2A Agent Card is served at
`http://127.0.0.1:18093/.well-known/agent-card.json` and the JSON-RPC calls are
on the [API overview](/docs/api-reference).

Stop a binary with Ctrl-C: `/readyz` flips to 503, it waits
`SHUTDOWN_DRAIN_DELAY` (5 s), drains HTTP, stops the consumers and the
scheduler, then the relay, and closes the pool. Remove the container with
`docker rm -f np-pg`.

## Building these docs

```bash
cd docs
npm ci
npm run build          # also fails on broken links and anchors
npm run gen-api-docs   # regenerate docs/api-reference/rest from apis/openapi.yaml
```

The REST reference pages are generated from `apis/openapi.yaml`; run the
generator after any spec change and commit the result, because `sidebars.ts`
imports the generated `sidebar.ts`.
