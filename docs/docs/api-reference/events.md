---
id: events
title: Event catalogue
sidebar_label: Event catalogue
sidebar_position: 2
description: "Every Kafka event Node Performance publishes and consumes: CloudEvents types, topics, keys and payload fields."
---

# Event catalogue

Every Kafka message this service produces or consumes is a CloudEvents 1.0 event in **structured
mode**: the Kafka value is the whole JSON envelope and the header `content-type` is
`application/cloudevents+json; charset=UTF-8`. Envelopes are built and decoded only in
`internal/adapters/kafka/cloudevents` ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)).
The contract is `apis/asyncapi.yaml` (AsyncAPI 3.0.0); field-level detail is on
[Domain events](/docs/ddd/domain-events). The architecture test `TestEventCatalogueMatchesContract`
fails the build if a type in the AsyncAPI document is missing from ADR 0004.

:::info Built
The outbox encoder and relay (`internal/adapters/outbound/{kafka,outbox}`) and the six consumers
(`internal/adapters/inbound/kafka`) are the only runtime of `cmd/api`. The tables below are the
pinned contract, not observed traffic; who consumes each published event is on
[Integration](/docs/ecosystem/integration).
:::

## Published

`source` `/retailnetwork/node-performance`, `subject` and Kafka key = `scorecardId`
(`nodeId:window`), `dataschema` `urn:retailnetwork:node-performance:<events|analytics>:<EventName>:v1`.
Each event goes to **exactly one** topic. The CloudEvents `id` is minted once, when the row is
appended to the outbox, so a relay retry republishes the same `id`.

| Type | Topic | Raised when | `data` |
| --- | --- | --- | --- |
| `com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated` | `retailnetwork.node-performance.events` | `Recompute` saved a scorecard whose state, grade or metrics changed (never `PROVISIONAL` to `PROVISIONAL`) | `nodeId`, `window`, `grade` (`A` to `D` or `PROVISIONAL`), `onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`, `promiseKeptRate`, `feedFreshnessP95Minutes`, `observations`, `computedAt` |
| `com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached` | `retailnetwork.node-performance.analytics` (analytics only) | a metric moved from the healthy to the unhealthy side of its bound between two `GRADED` recomputes; one event per metric | `nodeId`, `metric`, `value`, `threshold`, `window` |

All fields are always present. Timestamps are RFC 3339 UTC. Example (the contract's example):

```json
{
  "specversion": "1.0",
  "id": "7d0c6a52-3f7e-4b8e-9a51-2a8f0c1d9e44",
  "source": "/retailnetwork/node-performance",
  "type": "com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated",
  "subject": "node-0f3a91:30d",
  "time": "2026-10-22T14:03:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:node-performance:events:NodeScorecardUpdated:v1",
  "data": {
    "nodeId": "node-0f3a91", "window": "30d", "grade": "B",
    "onTimeHandoverRate": 0.962, "lateAckRate": 0.021, "rejectRate": 0.012,
    "expiryRate": 0.004, "promiseKeptRate": 0.951, "feedFreshnessP95Minutes": 38,
    "observations": 1874, "computedAt": "2026-10-22T14:03:00Z"
  }
}
```

Thresholds that raise `NodeThresholdBreached`: `onTimeHandoverRate` and `promiseKeptRate` below
0.90, `lateAckRate` above 0.05, `rejectRate` above 0.10, `expiryRate` above 0.05,
`feedFreshnessP95Minutes` above 240. A `GRADED` scorecard that falls back below 50 observations
publishes `NodeScorecardUpdated` with `grade` `PROVISIONAL` and no breach.

## Consumed

Six topics, 15 types, all decoded tolerantly (unknown fields and types ignored) and deduplicated on
the CloudEvents `id`. A message that fails is published to `<topic>.dlq`; see the
[Runbook](/docs/operations/runbook#dead-letter-queues).

| Type | Topic | Fields relied on |
| --- | --- | --- |
| `com.retailnetwork.partner.vendor-connectivity.purchaseorder.PurchaseOrderIssued` | `retailnetwork.vendor-connectivity.events` | `poNumber`, `nodeId`, `requiredShipBy`, `ackBy`; envelope `time` |
| `com.retailnetwork.partner.vendor-connectivity.purchaseorder.PurchaseOrderAccepted` | same | `poNumber`, `nodeId`, `acceptedAt` |
| `com.retailnetwork.partner.vendor-connectivity.purchaseorder.PurchaseOrderRejected` | same | `poNumber`, `nodeId`, `rejectedAt` |
| `com.retailnetwork.partner.vendor-connectivity.purchaseorder.PurchaseOrderExpired` | same | `poNumber`, `nodeId`, `ackBy` |
| `com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed` | same | `poNumber`, `nodeId`, `requiredShipBy` |
| `com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted` | same | `nodeId`, `shipmentId`, `receivedAt`, `cartons[].status` |
| `com.retailnetwork.partner.vendor-connectivity.vendoraccount.VendorOnboarded` | same | none (acknowledged) |
| `com.retailnetwork.partner.vendor-connectivity.submission.SubmissionProcessed` | same | none (acknowledged) |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedOver` | `retailnetwork.shipping-and-tracking.events` | `poNumber`, `nodeId`, `handedOverAt` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseKept` | `retailnetwork.delivery-promise.events` | `nodeId`, `deliveredAt` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseBroken` | same | `nodeId`, `cause`, optional `deliveredAt` |
| `com.retailnetwork.commerce.offer-availability.inventoryfeed.InventoryFeedApplied` | `retailnetwork.offer-availability.events` | `nodeId`, `appliedAt` |
| `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `retailnetwork.node-topology.events` | none (acknowledged) |
| `com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared` | same | none (acknowledged) |
| `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped` | `retailnetwork.seller-inbound.events` | `nodeId`, `shipmentId`, `shippedAt` |

Consumer groups come from environment variables, never from literals
(`TestKafkaConsumerGroupNeverHardcodedInline`); see
[Configuration](/docs/operations/configuration). No `warehouse.*` topic is ever read or written.
