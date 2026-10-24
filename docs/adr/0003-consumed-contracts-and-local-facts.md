# ADR 0003: consumed contracts, local facts, relationships and recompute

## Status

Accepted (2026-10-23).

## Context

node-performance is a measuring context: it consumes thirteen events from five
producers, publishes two, and serves two reads. It makes no synchronous call to a
sibling (`sync_calls: []`); the only in-organization caller is vendor-connectivity
reading `GET /scorecards/{nodeId}`.

## Decision

1. **Consumed contracts.** The inbound contract is restated, tolerantly, in
   `apis/asyncapi.yaml` (`receive` operations), built from each producer's
   `apis/asyncapi.yaml` on `origin/feature/service` (delivery-promise,
   node-topology, offer-availability, shipping-and-tracking, vendor-connectivity),
   re-checked at the end of the build. Only the fields below are required; unknown
   fields and unknown types are ignored; dispatch is on the FULL CloudEvents `type`.

   | Producer topic | Full type | Fields relied on | Effect |
   | --- | --- | --- | --- |
   | `retailnetwork.vendor-connectivity.events` | `com.retailnetwork.partner.vendor-connectivity.vendoraccount.VendorOnboarded` | `vendorId` | none (ADR 0002, A4) |
   | | `com.retailnetwork.partner.vendor-connectivity.purchaseorder.PurchaseOrderIssued` | `poNumber`, `nodeId`, `requiredShipBy`, `ackBy`, envelope `time` | upserts `PurchaseOrderFacts` |
   | | `...purchaseorder.PurchaseOrderAccepted` | `poNumber`, `nodeId`, `acceptedAt` | observation `ACK_ACCEPTED` |
   | | `...purchaseorder.PurchaseOrderRejected` | `poNumber`, `nodeId`, `rejectedAt` | observation `ACK_REJECTED` |
   | | `...purchaseorder.PurchaseOrderExpired` | `poNumber`, `nodeId`, `ackBy` | observation `ACK_EXPIRED` |
   | | `...purchaseorder.VendorShipmentConfirmed` | `poNumber`, `nodeId`, `requiredShipBy` | upserts `PurchaseOrderFacts` |
   | | `...submission.SubmissionProcessed` | none | none (A4) |
   | `retailnetwork.shipping-and-tracking.events` | `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedOver` | `poNumber`, `nodeId`, `handedOverAt` | observation `HANDOVER` |
   | `retailnetwork.delivery-promise.events` | `com.retailnetwork.commerce.delivery-promise.promise.PromiseKept` | `nodeId`, `deliveredAt` | observation `PROMISE_KEPT` |
   | | `...promise.PromiseBroken` | `nodeId`, `cause` | observation `PROMISE_BROKEN` for node-attributable causes (A8) |
   | `retailnetwork.offer-availability.events` | `com.retailnetwork.commerce.offer-availability.inventoryfeed.InventoryFeedApplied` | `nodeId`, `appliedAt` | observation `FEED_APPLIED` |
   | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `nodeId` | none (A4) |
   | | `...node.NodeOperationalProfileDeclared` | `nodeId` | none (A4) |

2. **Relationships.** Conformist toward every producer (the model): the local
   copies take the producers' shapes and never negotiate them. Customer/Supplier
   toward vendor-connectivity for `GET /scorecards/{nodeId}`: the supplier is this
   service, the contract is `apis/openapi.yaml`. delivery-promise and order-sourcing
   are downstream of `NodeScorecardUpdated` and call nothing here.
3. **Local facts.** One read model, `PurchaseOrderFacts` (table `purchase_order_facts`),
   keyed by `poNumber`: `nodeId`, `issuedAt`, `ackBy`, `requiredShipBy`. It is
   written by `PurchaseOrderIssued` and `VendorShipmentConfirmed` (a field is only
   ever filled, never erased) and read by the acknowledgement and handover
   use cases to judge lateness. It is a view of what the producers said, never an
   aggregate. An acceptance, rejection or handover whose facts are missing is a
   transient failure (`ErrPurchaseOrderUnknown`): the producers' events race on
   different topics, so the message is retried, then dead-lettered.
4. **Inbox.** Every consumer is idempotent on the CloudEvents `id`: the use case
   inserts the id into `processed_events` in the same Postgres transaction that
   applies the change (aggregate saves and facts upsert). A duplicate id makes
   the whole message a no-op that is acknowledged. Event types with no effect
   (A4) write only the inbox row.
5. **Consumer groups.** One stable group per consumed topic, each from a mandatory
   environment variable, read at boot before the listener binds:
   `VENDOR_CONNECTIVITY_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP`,
   `DELIVERY_PROMISE_CONSUMER_GROUP`, `OFFER_AVAILABILITY_CONSUMER_GROUP`,
   `NODE_TOPOLOGY_CONSUMER_GROUP` (ADR 0007 adds a sixth, `SELLER_INBOUND_CONSUMER_GROUP`). A message is committed only after its handler
   returned nil; a transient failure is retried with capped backoff and dead-lettered
   to `<topic>.dlq` after the attempt cap; a payload that can never be applied is
   dead-lettered at once. A message that is not a CloudEvents 1.0 event, or whose type
   has no handler, is skipped.
6. **Outbox publishing.** `Recompute` writes the scorecard and one `outbox_events`
   row per raised event, the CloudEvents envelope already encoded and its `id`
   minted once, in ONE Postgres transaction. An always-on relay drains the rows to
   Kafka with a capped attempt count (at-least-once, in id order, key = scorecardId).
7. **Recompute.** A scheduler in `cmd/api` runs the `Recompute` use case every
   `RECOMPUTE_INTERVAL` over the scorecards that are due (ADR 0002, A15); it is a
   worker with the same shutdown discipline as the consumers and the relay.
8. **Readiness.** `/readyz` answers 503 `dependency-unavailable` while draining,
   or when the Postgres ping or a Kafka metadata request (an empty, non-nil topic
   list) fails; `/healthz` is liveness only.

## Consequences

- The service boots only with `DATABASE_URL`, `KAFKA_BROKERS` and the five consumer
  group variables; a missing one is a boot error naming it.
- Adding a consumed event is an `apis/asyncapi.yaml` change plus a handler in
  `internal/adapters/inbound/kafka`; no shared code with the producer.
