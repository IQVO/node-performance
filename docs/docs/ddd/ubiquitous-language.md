---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 1
description: "The terms Node Performance speaks, mapped to code identifiers, plus the neighbour terms it reads."
---

# Ubiquitous language

The terms this context owns, with the Go identifier that carries each one
(`internal/domain/scorecard` unless noted), and the neighbour terms it only reads. Where a word means
something else in another context it is called out in the last column.

## This context's terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Scorecard** | A node's measured performance over a rolling window, with a grade. One per node and window: `scorecardId = nodeId + ':' + window`. | `NodeScorecard`, `ScorecardID(node, window)` | The **Operational Profile** (`node-topology`): what the vendor declares. A scorecard is never derived from it. |
| **Window** | The rolling period a scorecard covers: `7d` (7 days) or `30d` (30 days). | `Window`, `Window7d`, `Window30d`, `Windows()` | The **Ack Window** of a purchase order (`vendor-connectivity`), which is hours, not days. |
| **Observation** | One measured fact counted in a scorecard, keyed by the CloudEvents `id` of the event it came from. Kinds: `ACK_ACCEPTED`, `ACK_REJECTED`, `ACK_EXPIRED`, `HANDOVER`, `PROMISE_KEPT`, `PROMISE_BROKEN`, `FEED_APPLIED`. | `Observation`, `Kind` | A CloudEvents message: one event is at most one observation per scorecard, counted once (`ErrDuplicateObservation`). |
| **On-time handover rate** | On-time handovers over handovers; a handover is on time when `handedOverAt` is not after `requiredShipBy`. Higher is better; 1 when there is none. | `Metrics.OnTimeHandoverRate` | Promise-kept rate: handover is judged at the node's door, the promise at the customer's. |
| **Late ack rate** | Purchase orders acknowledged in the last 10% of their ack window, plus expired ones, over acknowledged or expired orders. Lower is better. | `Metrics.LateAckRate`, `AckIsLate` | Expiry rate, which counts only expired orders. |
| **Reject rate** | Rejected over acknowledged or expired purchase orders. | `Metrics.RejectRate` | A rejection by `order-sourcing` (only the vendor rejects). |
| **Expiry rate** | Expired over acknowledged or expired purchase orders. | `Metrics.ExpiryRate` | Late ack rate, which also counts late but in-time acknowledgements. |
| **Promise-kept rate** | Kept over kept plus node-attributable broken promises. Higher is better; 1 when there is none. | `Metrics.PromiseKeptRate`, `AttributableCause` | **Promise kept / broken** itself, `delivery-promise`'s judgement of one promise. |
| **Feed freshness p95** | Nearest-rank 95th percentile of the whole minutes between consecutive applied inventory feeds. Lower is better; 0 with fewer than two feeds. | `Metrics.FeedFreshnessP95Minutes` | `asOf` on a listing (`offer-availability`). |
| **Observations (count)** | The number of observations inside the window; a grade needs at least 50. | `Metrics.Observations`, `MinObservations` | The `Observation` entities themselves. |
| **State** | `PROVISIONAL` (below the minimum, no grade) or `GRADED`. | `State`, `StateProvisional`, `StateGraded` | `PROVISIONAL` as a promise state in `delivery-promise`. Same word, different context. |
| **Grade** | `A`, `B`, `C` or `D` from the grading bars; the wire value is `PROVISIONAL` while the state is. | `Grade`, `GradeFor`, `GradeLabel` | Node status (`ACTIVE`, `SUSPENDED`): a `D` grade never suspends a node. |
| **Grading policy** | The bars: `A` 0.98 on-time / 0.97 promise-kept / 0.01 late-ack, `B` 0.95 / 0.94 / 0.03, `C` 0.90 / 0.90 / 0.05, else `D`. | `gradingBars` | The threshold alert. |
| **Threshold breach** | A metric moving from the healthy to the unhealthy side of its bound between two graded states; reported per metric. | `Threshold`, `Breaches`, `NodeThresholdBreached` | A grade change: a breach is per metric, the grade is computed over three of them. |
| **Due** | A scorecard that has observations recorded since the last recompute, or whose oldest observation has left the window. | `NodeScorecard.DueForRecompute`, `ExpiresAt` | Overdue: nothing is late, a due scorecard is only waiting for the next tick. |
| **Purchase order facts** | The local copy of `issuedAt`, `ackBy` and `requiredShipBy` of a purchase order, needed to judge acknowledgements and handovers. | `PurchaseOrderFacts` | The purchase order itself (`vendor-connectivity`). |
| **Inbound timeliness / accuracy** | p95 minutes between a shipment's `shippedAt` and the `receivedAt` of its receipt; share of cartons received. Informational, never graded. | `InboundMetrics`, `ComputeInbound` | Feed freshness; a purchase-order reject rate. |
| **Declared vs Measured** | What a vendor says (profile) against what the network observes (scorecard). | (principle) | A comparison metric: no metric reads the declaration. |

## Neighbour terms this context uses

| Term | Definition (owner's) | Owner |
| --- | --- | --- |
| **Node** | One physical building of the network. Its id (`nodeId`) is copied from events, never looked up. | `node-topology` |
| **Operational Profile** | A node's declared hours, cutoffs, handling time and throughput cap. Never enters a metric here. | `node-topology` |
| **Vendor** | An organisation operating fulfillment nodes for the network. | `vendor-connectivity` |
| **Purchase Order (PO)** | The instruction to one vendor node to ship one shipment group in full by `requiredShipBy`. | `vendor-connectivity` |
| **Acknowledgement / Ack window / `ackBy`** | The vendor's whole-order accept or reject, and its deadline. | `vendor-connectivity` |
| **Required ship-by** | The pickup cutoff a purchase order must be ready for. | `vendor-connectivity` |
| **Handover** | The instant a vendor node's package enters the network. | `shipping-and-tracking` |
| **Inventory feed** | One vendor submission of availability for one node. Quantities are ignored here. | `offer-availability` |
| **Promise kept / broken** | The judgement at delivery against `deliverByLatest`; `cause` says why a promise broke. | `delivery-promise` |
| **Handicap** | The scorecard-driven delay `delivery-promise` applies to a node's ship-by. | `delivery-promise` |
| **Assignment cost** | The cost model in which `order-sourcing` weighs the grade. | `order-sourcing` |
| **Inbound shipment / receipt** | A seller's shipment to a vendor node, and the vendor's carton-level receipt of it. | `seller-inbound`, `vendor-connectivity` |

The canonical network and fleet term tables are in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/network/integration/wms-wes-integration).
This context has no edge to the warehouse-systems fleet.
