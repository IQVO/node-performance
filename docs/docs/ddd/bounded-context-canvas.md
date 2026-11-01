---
id: bounded-context-canvas
title: Bounded context canvas
sidebar_label: Bounded context canvas
sidebar_position: 2
description: "The ddd-crew Bounded Context Canvas v5 for Node Performance: purpose, classification, roles, messages, decisions and open questions."
---

# Bounded context canvas

Following the ddd-crew [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas),
filled in from the code of `develop`.

## 1. Name

**Node Performance** (`node-performance`, module `github.com/claudioed/node-performance`).

## 2. Purpose

Measure every node of the retail network against what was asked of it, over rolling 7-day and 30-day
windows, from observed facts only, and publish a grade. It keeps the network honest about what a
vendor *declared*: the declaration is what the vendor says, the scorecard is what the network
observes (**Declared vs Measured**).

## 3. Strategic classification

| Dimension | Value |
| --- | --- |
| Domain | [Supporting](/docs/ddd/subdomain-classification) (Performance Management), see [Core domain chart](/docs/ddd/core-domain-chart) |
| Business model | Risk reducer |
| Evolution | Custom-built |
| Tier / area | `partner`, retail network |

## 4. Domain roles

- **Execution context**: it computes the grades itself (recompute) and **analysis context** for the
  other contexts, which read the result. Vendors see their own scorecard only through
  `vendor-connectivity`.
- It is **not** a decision context: it grades, and `delivery-promise`, `order-sourcing` and people
  decide.

## 5. Inbound communication

| Message | Kind | From | Collaboration pattern |
| --- | --- | --- | --- |
| `PurchaseOrderIssued`, `PurchaseOrderAccepted`, `PurchaseOrderRejected`, `PurchaseOrderExpired`, `VendorShipmentConfirmed`, `InboundReceiptSubmitted`, `VendorOnboarded`, `SubmissionProcessed` | events | `vendor-connectivity` | Conformist |
| `PackageHandedOver` | event | `shipping-and-tracking` | Conformist |
| `PromiseKept`, `PromiseBroken` | events | `delivery-promise` | Conformist |
| `InventoryFeedApplied` | event | `offer-availability` | Conformist |
| `NodeRegistered`, `NodeOperationalProfileDeclared` | events (no effect) | `node-topology` | Conformist |
| `InboundShipmentShipped` | event | `seller-inbound` | Conformist |
| `GetScorecard` (`GET /scorecards/{nodeId}`) | query | `vendor-connectivity` | Customer/Supplier, this context is the Supplier |
| `get_scorecard`, `list_scorecards_by_grade` | MCP queries | agents | Open host, read-only |
| `node_performance`, `nodes_by_grade` | A2A skills | agents | Open host, read-only |

## 6. Outbound communication

| Message | Kind | To | Channel |
| --- | --- | --- | --- |
| `NodeScorecardUpdated` | event | `delivery-promise`, `order-sourcing` | Kafka `retailnetwork.node-performance.events` |
| `NodeThresholdBreached` | event | analytics (no in-organization consumer) | Kafka `retailnetwork.node-performance.analytics` |

No command and no query leaves the context: it calls no sibling.

## 7. Ubiquitous language

Scorecard, Window, Observation, Grade, On-time handover rate, Late ack rate, Provisional, Threshold
breach, Declared vs Measured; see [Ubiquitous language](/docs/ddd/ubiquitous-language).

## 8. Business decisions

1. A scorecard is computed only from observed facts, never from the vendor's declaration.
2. A node with fewer than 50 observations in the window is `PROVISIONAL`, not `A`.
3. Grades are `A` to `D` by the grading bars over on-time handover, promise-kept and late-ack rates.
4. A metric that crosses its bound downward between two graded states raises `NodeThresholdBreached`.
5. Only node-attributable broken promises (`LATE`, `LATE_HANDOVER`, `VENDOR_REJECT`, `ACK_EXPIRED`)
   count against the node; `LOST` and `RETURNED` do not.
6. An acknowledgement in the last tenth of its ack window is late; an expired order is always late.
7. This context never suspends a node and never changes a promise.

## 9. Assumptions

The numbered assumptions A1 to A23 live in
[ADR 0002](/docs/adr/0002-aggregates-and-invariants) and
[ADR 0003](/docs/adr/0003-consumed-contracts-and-local-facts); the ones that shape behaviour: the
first observation creates both scorecards (A1), a metric with an empty denominator reports its best
value (A5), the instant of a fact is the fact's own timestamp, not the envelope's (A3), the boundary
instant of `requiredShipBy` is on time, and the read routes take no `Idempotency-Key` (A21).

## 10. Verification metrics

| Metric | Where |
| --- | --- |
| `nodeperformance.scorecards.recomputed` by `outcome` and `reason` | [Observability](/docs/operations/observability#metrics) |
| Unpublished outbox rows, attempts, consumer lag, DLQ traffic | SQL and Kafka tooling, see the [Runbook](/docs/operations/runbook) |
| Dashboard `warehouse-node-performance` and the Kong SLO alerts | warehouse-infra |
| Coverage 90%, mutation efficacy 95 (measured 100) | [Testing](/docs/development/testing) |

## 11. Open questions and discrepancies

- `VendorOnboarded`, `SubmissionProcessed`, `NodeRegistered` and `NodeOperationalProfileDeclared` are
  consumed with no effect (inbox row only). The declared profile never enters a metric, so there is
  no "declared next to measured" read model.
- The informational inbound metrics are on REST reads but not on MCP, A2A or the event.
- Inbound accuracy mixes node and seller causes until they are separated (model gap G52).
