---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 10
description: "The two events Node Performance publishes and the fifteen it consumes, with full CloudEvents types, payloads, topics and idempotency rules."
---

# Domain events

Every event this context publishes and consumes, with the envelope rules of
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). The summary tables are on the
[Event catalogue](/docs/api-reference/events); this page carries the field-level detail.

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once when the event is appended to the outbox; a redelivery carries the same `id` |
| `source` | `/retailnetwork/node-performance` |
| `type` | `com.retailnetwork.partner.node-performance.scorecard.<EventName>` |
| `subject` | the aggregate root id, `scorecardId = nodeId:window` (for example `N-0042:30d`) |
| `time` | the recompute instant (`computedAt`) |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:node-performance:<events\|analytics>:<EventName>:v1` |
| Kafka key | the `scorecardId`, same as `subject` |
| Kafka header | `content-type: application/cloudevents+json; charset=UTF-8` (plus `traceparent`/`tracestate` when a span was active) |

Source: `internal/adapters/kafka/cloudevents/cloudevents.go` (`Type`, `DataSchema`, `New`),
`internal/adapters/outbound/kafka/encoder.go` (`Encoder.Encode`).

## Published

### NodeScorecardUpdated

| | |
| --- | --- |
| **Type** | `com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated` |
| **Topic** | `retailnetwork.node-performance.events` (and only that topic) |
| **Produced by** | `Recompute` after the scorecard is saved (outbox row in the same transaction) |
| **Raised when** | state, grade or metrics changed; never `PROVISIONAL` to `PROVISIONAL`; also `GRADED` back to `PROVISIONAL` |
| **Consumers** | `delivery-promise` (scorecard view and handicap), `order-sourcing` (scorecard view and assignment cost) |

| Field | Type | Meaning |
| --- | --- | --- |
| `nodeId` | string | The node, as minted by `node-topology`. |
| `window` | string | `7d` or `30d`. |
| `grade` | string | `A`, `B`, `C`, `D`, or `PROVISIONAL` while the state is. |
| `onTimeHandoverRate` | number in `[0,1]` | On-time handovers over handovers. |
| `lateAckRate` | number in `[0,1]` | Late acknowledgements plus expiries over acknowledged or expired orders. |
| `rejectRate` | number in `[0,1]` | Rejected over acknowledged or expired orders. |
| `expiryRate` | number in `[0,1]` | Expired over acknowledged or expired orders. |
| `promiseKeptRate` | number in `[0,1]` | Kept over kept plus node-attributable broken promises. |
| `feedFreshnessP95Minutes` | integer, minutes | Nearest-rank p95 of the gaps between applied inventory feeds. |
| `observations` | integer | Observations counted in the window. |
| `computedAt` | RFC 3339 instant | When the recompute ran. |

The two inbound metrics are **not** on this event.

### NodeThresholdBreached

| | |
| --- | --- |
| **Type** | `com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached` |
| **Topic** | `retailnetwork.node-performance.analytics` (analytics only, never the integration topic; dataschema stream `analytics`) |
| **Produced by** | `Recompute`, one event per metric that moved from the healthy to the unhealthy side of its bound between two `GRADED` states |
| **Consumers** | none in the organization |

| Field | Type | Meaning |
| --- | --- | --- |
| `nodeId` | string | The node. |
| `metric` | string | The metric that crossed: `onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`, `promiseKeptRate` or `feedFreshnessP95Minutes`. |
| `value` | number | The metric's new value. |
| `threshold` | number | The bound it crossed. |
| `window` | string | `7d` or `30d`. |

## Consumed

Every consumer dispatches on the full `type` string and is idempotent on the CloudEvents `id`: the
`processed_events` inbox row and the effect commit in one transaction. A duplicate `id` is
acknowledged and skipped; behind it `ErrDuplicateObservation` stops a second count of the same source
event inside a scorecard. Each topic has its own consumer group
([Runbook](/docs/operations/runbook#kafka-topics-and-consumer-groups)).

| Producer | Full `type` | Fields used | What this context does |
| --- | --- | --- | --- |
| `vendor-connectivity` | `com.retailnetwork.partner.vendor-connectivity.vendoraccount.VendorOnboarded` | none | Acknowledged: inbox row only. |
| `vendor-connectivity` | `...purchaseorder.PurchaseOrderIssued` | `poNumber`, `nodeId`, `requiredShipBy`, `ackBy`; envelope `time` as `issuedAt` | Upserts the purchase-order facts; no observation. |
| `vendor-connectivity` | `...purchaseorder.PurchaseOrderAccepted` | `poNumber`, `nodeId`, `acceptedAt` | `ACK_ACCEPTED` observation, late in the last tenth of the ack window. |
| `vendor-connectivity` | `...purchaseorder.PurchaseOrderRejected` | `poNumber`, `nodeId`, `rejectedAt` | `ACK_REJECTED` observation (an acknowledgement and a rejection). |
| `vendor-connectivity` | `...purchaseorder.PurchaseOrderExpired` | `poNumber`, `nodeId`, `ackBy` | `ACK_EXPIRED` observation, always late. |
| `vendor-connectivity` | `...purchaseorder.VendorShipmentConfirmed` | `poNumber`, `nodeId`, `requiredShipBy` | Fills `requiredShipBy` of the facts; the vendor's own `shippedAt` is not used. |
| `vendor-connectivity` | `...submission.SubmissionProcessed` | none | Acknowledged: inbox row only. |
| `vendor-connectivity` | `...inboundreceipt.InboundReceiptSubmitted` | `nodeId`, `shipmentId`, `receivedAt`, `cartons[].status` | Stores carton counts; informational inbound metrics. |
| `shipping-and-tracking` | `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedOver` | `poNumber`, `nodeId`, `handedOverAt` | `HANDOVER` observation, on time if not after `requiredShipBy`. |
| `delivery-promise` | `com.retailnetwork.commerce.delivery-promise.promise.PromiseKept` | `nodeId`, `deliveredAt` | `PROMISE_KEPT` observation. |
| `delivery-promise` | `...promise.PromiseBroken` | `nodeId`, `cause`, optional `deliveredAt` | `PROMISE_BROKEN` observation only for `LATE`, `LATE_HANDOVER`, `VENDOR_REJECT`, `ACK_EXPIRED`; otherwise ignored. |
| `offer-availability` | `com.retailnetwork.commerce.offer-availability.inventoryfeed.InventoryFeedApplied` | `nodeId`, `appliedAt` | `FEED_APPLIED` observation. |
| `node-topology` | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | none | Acknowledged; a node's scorecards are created by its first observation. |
| `node-topology` | `...node.NodeOperationalProfileDeclared` | none | Acknowledged; the declared profile never enters a metric. |
| `seller-inbound` | `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped` | `nodeId`, `shipmentId`, `shippedAt` | Stores the shipped instant (first wins). |

Source: `internal/adapters/inbound/kafka/handlers.go` (the type constants, payload structs and
handlers), `internal/application/usecases/intake.go`. Partition order: each producer keys by its own
aggregate id, so facts about one node arrive on many partitions in no global order; a scorecard does
not depend on arrival order except that acknowledgements and handovers need their purchase-order
facts first (retried, then dead-lettered).
