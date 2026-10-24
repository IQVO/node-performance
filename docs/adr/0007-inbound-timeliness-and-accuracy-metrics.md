# ADR 0007: inbound timeliness and accuracy, two informational scorecard metrics

## Status

Accepted (2026-10-24). Design source: `warehouse-docs` `network/model/model.yaml`, context
`node-performance` (value object `Metrics`, policy `InboundObservations`) and the mastercase
contracts, section 10 (P3, P6).

## Context

Phase 4 lets sellers ship cartons to a node. Two producers say what happened to a shipment:
seller-inbound's `InboundShipmentShipped` (when it left) and vendor-connectivity's
`InboundReceiptSubmitted` (what the node's dock reported, carton by carton, and when). The model
adds `inboundTimelinessP95Minutes` and `inboundAccuracyRate` to the scorecard `Metrics` and states
that they are "informational: not an input to Grading and not on NodeScorecardUpdated".

## Decision

1. **Informational only.** The grade (A to D), `MinObservations`, the scorecard `observations`
   count, the `PROVISIONAL`/`GRADED` state, `NodeScorecardUpdated` and `NodeThresholdBreached` are
   unchanged. The model is explicit, so nothing is left to an assumption here: no inbound fact
   becomes a scorecard `Observation`, no scorecard is created or marked dirty by one, and no event
   is published for one.
2. **Inputs.**
   - `com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted` on the
     existing vendor-connectivity topic and group. Relied on: `shipmentId`, `nodeId`, `receivedAt`,
     `cartons[].status`. Everything else (`transactionId`, `vendorId`, `items`, `complianceCodes`, ...)
     is ignored.
   - `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped` on
     `retailnetwork.seller-inbound.events` under a NEW consumer group read from the mandatory
     `SELLER_INBOUND_CONSUMER_GROUP` (chart value `config.sellerInboundConsumerGroup`). Relied on:
     `shipmentId`, `nodeId`, `shippedAt`. The producer was not merged when this was built: the
     consumer follows the written contract (section 10, P3) with a tolerant local copy in
     `apis/asyncapi.yaml`; re-check it against the producer's AsyncAPI when it lands.
3. **Local read models** (migration `0003`): `inbound_shipments(shipment_id PK, node_id, shipped_at)`
   and `inbound_receipts(shipment_id PK, node_id, received_at, cartons_total, cartons_received)`.
   The first fact stored per `shipmentId` wins (`ON CONFLICT DO NOTHING`), in both tables.
4. **Join and parking.** The read joins receipt and shipment by `shipmentId` only. A receipt that
   arrives before its `InboundShipmentShipped` is simply stored and not returned by the (inner)
   join; it counts from the moment the shipment arrives. It is never dropped and never counted
   twice: one receipt per shipment counts (a second report of the same shipment, under another
   CloudEvents id, is a duplicate outcome), and a redelivered event is stopped by the inbox
   (`processed_events`, keyed by the CloudEvents id) in the same transaction. The receipt is
   attributed to the node of the RECEIPT.
5. **Timeliness.** One sample per joined receipt: `receivedAt - shippedAt` truncated to whole
   minutes. **Negative durations are clamped to 0**, not rejected: two producers' clocks can
   disagree by a few minutes, and the carton counts of the receipt are real facts that must not
   be lost. The metric is the nearest-rank p95 (`ceil(0.95 * n)`-th smallest, the rule of A9);
   one sample is its own p95. Lower is better.
6. **Accuracy.** Cartons whose status is exactly `RECEIVED` over all cartons reported, POOLED
   over the receipts of the window (not a mean of per-receipt rates, so a ten-carton receipt
   weighs ten times a one-carton receipt). `MISSING`, `UNEXPECTED`, `DAMAGED` and any unknown
   status count as not received. A receipt with no cartons adds a timeliness sample and nothing
   to the accuracy. Higher is better.
7. **Window.** Same rule as A3: a receipt counts in a window at `now` when its `receivedAt` is
   after `now - window` (a future instant counts). The 7d and 30d scorecards of a node each get
   their own value.
8. **Derived at read time.** The two metrics are computed when the scorecard is read
   (`GET /scorecards/{nodeId}` and `GET /scorecards?grade=`), from the joined read models and the
   service clock, not stored on the `scorecards` row. Hence they need no recompute, change no
   aggregate version and are always as fresh as the last consumed event, while the other metrics
   stay as of `computedAt`. The list read fetches all nodes of the page in one query.
9. **Wire.** `Metrics` gains the optional `inboundTimelinessP95Minutes` (integer, minutes) and
   `inboundAccuracyRate` (number in [0,1]) in `apis/openapi.yaml`, OMITTED when the window has no
   joined receipt (timeliness) or no carton (accuracy): "no data" is not a best value here,
   unlike A5, because an absent measurement must not read as a perfect one. Additive, so existing
   consumers are unaffected. `apis/asyncapi.yaml` gains the two consumed messages and the
   seller-inbound channel; `NodeScorecardUpdated` is unchanged.

## Consequences

- Six consumers now run (was five); the shutdown budget in `cmd/api` awaits one more worker, and
  the chart refuses to render without the sixth group id.
- A vendor-connectivity receipt for a shipment seller-inbound never announces stays stored and
  invisible: bounded by the number of receipts, no expiry yet. An `inbound_*` retention sweep is a
  follow-up if volume ever matters.
- If the model later wants inbound to feed the grade, that is a new ADR: the facts would have to
  become `Observation`s (counted towards `MinObservations`) and a threshold table entry (A11).
