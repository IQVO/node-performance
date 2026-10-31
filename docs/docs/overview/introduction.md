---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What Node Performance owns and does not own, its Supporting subdomain class in the retail network, its capabilities and where to read next."
---

# Node Performance

`node-performance` is a **Supporting** bounded context of the **retail network** (partner
tier, CloudEvents segment `partner`). The network does not run its fulfillment nodes: vendors do,
and every promise the network makes rests on what a vendor *declared* about a node. This
context answers one question in a repeatable way: *how has this node actually behaved over the
last 7 and 30 days?* It answers from **observed facts only**, never from the declaration (the
model's principle of *Declared vs Measured*), and publishes the result as a grade.

It consumes what the network observed (purchase orders issued, accepted, rejected and expired,
packages handed over against `requiredShipBy`, promises kept or broken, inventory feeds
applied, inbound shipments and receipts), keeps two `NodeScorecard` aggregates per node (a 7-day
and a 30-day one), computes six graded metrics, grades the node `A` to `D` from 50 observations
(`PROVISIONAL` below), raises `NodeThresholdBreached` when a metric crosses its bound, and
publishes `NodeScorecardUpdated` for the contexts that act on the grade.

:::info What runs on develop
Three binaries: `cmd/api` (a chi REST adapter with two read routes, six Kafka consumers, a
recompute scheduler and an always-on transactional-outbox relay, all in one process), a read-only
MCP server (`cmd/mcp`, [ADR 0007](/docs/adr/0007-mcp-server-adoption)) and a read-only A2A agent
(`cmd/a2a`, [ADR 0011](/docs/adr/0011-a2a-inbound-adapter)). It runs on real Postgres and real
Kafka only: there is no in-memory mode, and `DATABASE_URL`, `KAFKA_BROKERS` and the six consumer
group ids are mandatory ([ADR 0006](/docs/adr/0006-http-adapter-consumers-and-readiness)). Also
packaged: a Helm chart (`charts/node-performance`) and a console remote (`web/`, the
`node_performance_mfe` Module Federation remote, [ADR 0008](/docs/adr/0008-console-remote)).
:::

## What the context owns

| Concept | Where it lives | Rule in one line |
| --- | --- | --- |
| **NodeScorecard** (two per node, `7d` and `30d`) | `internal/domain/scorecard` | State `PROVISIONAL` or `GRADED`, grade `A` to `D`, metrics, observations; saved with a version guard ([ADR 0002](/docs/adr/0002-aggregates-and-invariants)). |
| **Observation** | `internal/domain/scorecard/observation.go` | One measured fact keyed by the CloudEvents id of its source event; kinds `ACK_ACCEPTED`, `ACK_REJECTED`, `ACK_EXPIRED`, `HANDOVER`, `PROMISE_KEPT`, `PROMISE_BROKEN`, `FEED_APPLIED`. A source id counts once. |
| **Grading policy** | `internal/domain/scorecard/grade.go` | `A`: on-time ≥ 0.98, promise-kept ≥ 0.97, late-ack ≤ 0.01; `B`: 0.95 / 0.94 / 0.03; `C`: 0.90 / 0.90 / 0.05; otherwise `D`. Never below 50 observations. |
| **Threshold alert** | same file | Six bounds (on-time ≥ 0.90, promise-kept ≥ 0.90, late-ack ≤ 0.05, reject ≤ 0.10, expiry ≤ 0.05, feed freshness p95 ≤ 240 min); a downward crossing between two graded states raises `NodeThresholdBreached`. |
| **PurchaseOrderFacts** | `purchase_order_facts` table | Local copy of `issuedAt`, `ackBy` and `requiredShipBy` per purchase order, needed to judge acknowledgements and handovers ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-facts)). Not aggregate state. |
| **Inbound shipments and receipts** | `inbound_shipments`, `inbound_receipts` | Joined by `shipmentId` into two informational metrics, `inboundTimelinessP95Minutes` and `inboundAccuracyRate`, shown on reads and never graded ([ADR 0009](/docs/adr/0009-inbound-timeliness-and-accuracy-metrics)). |

## What it does not own

- **Orders, promises, inventory, topology, shipments.** They belong to `order-sourcing`,
  `delivery-promise`, `offer-availability`, `node-topology` and `shipping-and-tracking`; here they are
  only observed facts that arrive as events. Facts are never fetched from a sibling: the
  service calls no other context over REST, MCP or A2A.
- **The promise handicap and the choice of a node.** `delivery-promise` and `order-sourcing`
  read the grade from `NodeScorecardUpdated`; this context publishes a grade and stops.
- **Suspending or blocking a node.** That is a `node-topology` decision made by people.
- **The vendor's declaration.** A low on-time rate does not rewrite the operational profile.
- **Penalties and invoicing**, and **metrics for network-operated nodes** (sortation centres,
  delivery stations), which receive no purchase orders and send no feeds.
- **Authentication.** REST, MCP and A2A are unauthenticated fleet-wide; the in-cluster ClusterIP
  boundary is the access control.

## Classification and area

- **Supporting subdomain.** The grading rule is a replaceable policy over facts the network
  already produces; the competitive core of the network is elsewhere
  ([Subdomain classification](/docs/ddd/subdomain-classification)).
- **Area: Network (retail-network)**, partner tier: event types look like
  `com.retailnetwork.partner.node-performance.scorecard.<EventName>`. This service belongs to the
  retail network, not to the warehouse-systems fleet; the fleet reaches the network only through the
  `vendor-connectivity` Vendor API.
- The repository is a domain-driven-design study, not associated with any company.

## Main capabilities

1. Consume 15 event types from six producers on six topics, deduplicated on the CloudEvents `id`
   with dead-lettering ([Integration](/docs/ecosystem/integration)).
2. Record observations on both windows of the node; the first observation creates the scorecards.
3. Recompute the due scorecards every minute: drop observations that left the window, recompute
   six metrics, grade, and detect threshold crossings
   ([Use cases](/docs/ddd/use-cases)).
4. Publish `NodeScorecardUpdated` (topic `retailnetwork.node-performance.events`) and
   `NodeThresholdBreached` (topic `retailnetwork.node-performance.analytics`) through a
   transactional outbox with an attempt cap ([Event catalogue](/docs/api-reference/events)).
5. Serve the scorecards read-only over REST (`GET /scorecards/{nodeId}`,
   `GET /scorecards?grade=`), MCP (`get_scorecard`, `list_scorecards_by_grade`) and A2A (skills
   `node_performance`, `nodes_by_grade`) ([API overview](/docs/api-reference)).

## Where to go next

| You want to | Read |
| --- | --- |
| See the layers, binaries, diagram and fitness rules | [Architecture](/docs/overview/architecture), [Runtime](/docs/overview/runtime) |
| Run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| Know every setting | [Configuration](/docs/operations/configuration) |
| Deploy and operate it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Before first release](/docs/operations/before-first-release) |
| Fix something that broke | [Troubleshooting](/docs/operations/troubleshooting) |
| Know what tests and CI gates exist | [Testing](/docs/development/testing) |
| See who talks to whom | [Integration](/docs/ecosystem/integration), [MCP tools](/docs/mcp/tools) |
| Read the design | [Ubiquitous language](/docs/ddd/ubiquitous-language), [Use cases](/docs/ddd/use-cases), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| Read the contracts | [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events) |
| Read the decisions | [ADR index](/docs/adr) |

Published at [https://iqvo.github.io/node-performance/](https://iqvo.github.io/node-performance/).
