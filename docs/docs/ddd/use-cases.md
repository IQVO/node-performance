---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 1
description: "Every application use case of Node Performance: trigger, inputs, invariants, events raised, errors and idempotency."
---

# Use cases

The use cases live in `internal/application/usecases`: `Intake` (one method per consumed event
type), `Recompute` and `Queries`. Every command follows one shape inside **one**
`ports.UnitOfWork`: load, call the domain, save, append the raised events to the outbox. A lost
optimistic-concurrency race (`ports.ErrConcurrentUpdate`) reruns the whole attempt (inbox row,
load, domain call, save, outbox) on fresh state, up to 5 times (`maxAttempts`,
`internal/application/usecases/writer.go`).

No use case has a REST route: the facts are driven by Kafka and the recompute by a timer. The two
REST routes, the two MCP tools and the two A2A skills are all the same two queries. The
step-by-step runtime view is in [Sequence diagrams](/docs/ddd/sequence-diagrams).

## Triggers at a glance

| Use case | Trigger | Inbound adapter | Events raised |
| --- | --- | --- | --- |
| `Intake.PurchaseOrderIssued` | `PurchaseOrderIssued` | `inbound/kafka` (vendor-connectivity consumer) | none |
| `Intake.ShipmentConfirmed` | `VendorShipmentConfirmed` | same | none |
| `Intake.PurchaseOrderAcknowledged` | `PurchaseOrderAccepted`, `PurchaseOrderRejected` | same | none |
| `Intake.PurchaseOrderExpired` | `PurchaseOrderExpired` | same | none |
| `Intake.PackageHandedOver` | `PackageHandedOver` | shipping-and-tracking consumer | none |
| `Intake.PromiseKept`, `Intake.PromiseBroken` | `PromiseKept`, `PromiseBroken` | delivery-promise consumer | none |
| `Intake.InventoryFeedApplied` | `InventoryFeedApplied` | offer-availability consumer | none |
| `Intake.InboundShipmentShipped` | `InboundShipmentShipped` | seller-inbound consumer | none |
| `Intake.InboundReceiptSubmitted` | `InboundReceiptSubmitted` | vendor-connectivity consumer | none |
| `Intake.Acknowledge` | `VendorOnboarded`, `SubmissionProcessed`, `NodeRegistered`, `NodeOperationalProfileDeclared` | vendor-connectivity and node-topology consumers | none |
| `Recompute.Run` | timer, every `RECOMPUTE_INTERVAL` (default 1m) and once at boot | `cmd/api/workers.go` scheduler | `NodeScorecardUpdated`, `NodeThresholdBreached` |
| `Queries.GetScorecard` | `GET /scorecards/{nodeId}`, MCP `get_scorecard`, A2A `node_performance` | REST, MCP, A2A | none |
| `Queries.ListScorecardsByGrade` | `GET /scorecards?grade=`, MCP `list_scorecards_by_grade`, A2A `nodes_by_grade` | REST, MCP, A2A | none |

## Intake (the common shape)

Every intake method runs `Intake.apply`:

1. Reject an event without a CloudEvents id of 1 to 200 characters or without a type
   (`ErrInvalidEvent`).
2. In one unit of work, `Inbox.MarkProcessed(id, type, now)`. If the id is already in
   `processed_events` the outcome is `duplicate` and nothing else happens.
3. Otherwise run the effect (below) and commit; the consumer then commits its Kafka offset.

Outcomes: `applied`, `duplicate`, `ignored`. A domain value the model refuses
(`shared.ErrValidation`) becomes `ErrInvalidEvent`, which the consumer dead-letters at once.
Idempotency is the inbox on the CloudEvents `id`; behind it, `ErrDuplicateObservation` stops the
same source event id being counted twice inside a scorecard.

### `PurchaseOrderIssued` and `ShipmentConfirmed`: remember the purchase order

- **Input**: `poNumber`, `nodeId`, `requiredShipBy`, `ackBy` (issue) or `poNumber`, `nodeId`,
  `requiredShipBy` (shipment); the issue instant is the envelope `time`.
- **Invariants**: `PurchaseOrderFacts.Validate`; facts are merged (`Merge`), so either event can
  arrive first. They record **no observation** (ADR 0002, A2).
- **Events**: none.

### `PurchaseOrderAcknowledged` (accepted or rejected)

- **Input**: `poNumber`, `nodeId`, `acceptedAt` or `rejectedAt`.
- **Invariants**: the purchase order's `issuedAt` and `ackBy` must be known
  (`CanJudgeAck`), else `ErrPurchaseOrderUnknown` (transient, retried then dead-lettered). The
  acknowledgement is **late** when it falls in the last tenth of `[issuedAt, ackBy]` (or at/after `ackBy`).
- **Effect**: `ACK_ACCEPTED` / `ACK_REJECTED` observation recorded on both windows.

### `PurchaseOrderExpired`

- **Input**: `poNumber`, `nodeId`, `ackBy`. **Effect**: `ACK_EXPIRED` observation at `ackBy`, always late.

### `PackageHandedOver`

- **Input**: `poNumber`, `nodeId`, `handedOverAt`.
- **Invariants**: `requiredShipBy` must be known (`CanJudgeHandover`), else `ErrPurchaseOrderUnknown`.
  On time when `handedOverAt` is not after `requiredShipBy` (the boundary instant is on time).
- **Effect**: `HANDOVER` observation.

### `PromiseKept` and `PromiseBroken`

- **Input**: `nodeId`, `deliveredAt` (and `cause` for broken).
- **Invariants**: a broken promise counts only for the node-attributable causes `LATE`,
  `LATE_HANDOVER`, `VENDOR_REJECT`, `ACK_EXPIRED`; any other cause is `ignored`. A missing `deliveredAt`
  falls back to the envelope time.
- **Effect**: `PROMISE_KEPT` / `PROMISE_BROKEN` observation.

### `InventoryFeedApplied`

- **Input**: `nodeId`, `appliedAt`. **Effect**: `FEED_APPLIED` observation (feed freshness).

### Recording an observation (shared by the five above)

`Intake.record` counts the observation on **both** windows of the node: for each of `7d` and `30d` it
loads the scorecard by `nodeId:window`, creates it from the first observation if it does not exist
(`scorecard.Open`: `PROVISIONAL`, version 1, due for recompute), or calls
`NodeScorecard.RecordObservation` (which marks it dirty and bumps the version). A source event id
already counted on every window yields `duplicate`. `RecordObservation` raises **no event**; the
scorecard is due for the next recompute.

### Inbound shipment and receipt (informational)

- `InboundShipmentShipped` (`shipmentId`, `nodeId`, `shippedAt`): stores the shipped instant; the
  first instant wins, a stored shipment is a `duplicate`. No scorecard observation.
- `InboundReceiptSubmitted` (`shipmentId`, `nodeId`, `receivedAt`, cartons' `status`): stores the
  carton counts (`RECEIVED` over total); a receipt whose shipment has not been seen is parked and
  joined by the read side when the shipment arrives; the first receipt of a shipment wins.
- These feed `inboundTimelinessP95Minutes` and `inboundAccuracyRate` on reads only
  ([ADR 0009](/docs/adr/0009-inbound-timeliness-and-accuracy-metrics)); they are never graded and
  never published.

### `Acknowledge`

Writes only the inbox row for events with no effect (`VendorOnboarded`, `SubmissionProcessed`,
`NodeRegistered`, `NodeOperationalProfileDeclared`); outcome `ignored`.

## Recompute

- **Trigger**: the scheduler in `cmd/api/workers.go`, once at boot and every `RECOMPUTE_INTERVAL`.
- **Input**: none; it asks `Scorecards.ListDue(now, 200)`, up to 10 rounds per run.
- **Per scorecard**, in its own unit of work: load; skip if it vanished or is no longer due
  (`DueForRecompute`: dirty, or its oldest observation has left the window); call
  `NodeScorecard.Recompute(now)`; save; append the returned events to the outbox in the same
  transaction.
- **Domain rules** (`internal/domain/scorecard/scorecard.go`, `grade.go`, `metrics.go`):
  1. drop observations at or before `now - window`;
  2. compute the metrics: `onTimeHandoverRate` = on-time handovers / handovers (1 if none),
     `lateAckRate` = (late + expired) / acknowledged-or-expired (0), `rejectRate`, `expiryRate`
     over the same denominator (0), `promiseKeptRate` = kept / (kept + broken) (1),
     `feedFreshnessP95Minutes` = nearest-rank p95 of the whole minutes between consecutive feeds
     (0 with fewer than two), `observations` = the count;
  3. below `MinObservations` = 50 the state is `PROVISIONAL` with no grade; otherwise `GRADED` and
     the grade is the first of `A` (0.98 / 0.97 / 0.01), `B` (0.95 / 0.94 / 0.03), `C` (0.90 /
     0.90 / 0.05) that the on-time, promise-kept and late-ack rates meet, else `D`.
- **Events raised**: `NodeScorecardUpdated` when the state, grade or metrics changed (never
  `PROVISIONAL` to `PROVISIONAL`); then, only between two `GRADED` states, one
  `NodeThresholdBreached` per metric that crossed its bound downward (on-time and promise-kept below
  0.90, late-ack above 0.05, reject above 0.10, expiry above 0.05, feed freshness above 240
  minutes). The bound itself is healthy.
- **Errors**: a lost race counts `concurrent-update` and the scorecard stays due; any other error ends
  the run, is logged `recompute round failed` and counted `internal-error`; the next tick retries.
- **Idempotency**: the outcome is a function of the observations and `now`; a run with nothing
  changed raises no event.
- **Metric**: each scorecard handled increments `nodeperformance.scorecards.recomputed`
  ([Observability](/docs/operations/observability#metrics)).

## Queries

| Use case | Input | Rules | Errors |
| --- | --- | --- | --- |
| `GetScorecard` | `nodeId` (1 to 64 characters from `A-Z a-z 0-9 . _ : -`, starting with a letter or digit) | Returns both scorecards (7d then 30d) and adds the two informational inbound metrics per window. | A malformed id is `validation-failed` (422); a node nothing was observed about is `not-found` (404). |
| `ListScorecardsByGrade` | `grade` (required: `A`, `B`, `C`, `D`, `PROVISIONAL`), `window` (`7d`, `30d`), `limit` (1 to 500, default 100), `after` (cursor, at most 80 characters) | Ordered by `scorecardId` ascending; keyset paging; adds the inbound metrics. | A bad `grade`, `window`, `limit` or cursor is `validation-failed` (422). |

Both are read-only, so neither takes an `Idempotency-Key` (ADR 0002, A21).

## Errors and the problem types they become

| Error | REST problem `type` and status | Consumer behaviour |
| --- | --- | --- |
| `ports.ErrNotFound` | `not-found` 404 | n/a |
| `shared.ErrValidation` (query side) | `validation-failed` 422 | n/a |
| `ErrInvalidEvent` (intake side) | n/a | dead-letter at once |
| `ErrPurchaseOrderUnknown` | n/a | retry up to 5 attempts, then dead-letter |
| `ports.ErrConcurrentUpdate` | `concurrent-update` 409 | rerun the unit of work up to 5 times, then retry the message |
| `scorecard.ErrDuplicateObservation` | n/a | treated as success: `duplicate` |
| `scorecard.ErrRateOutOfRange`, `ErrInsufficientObservations` | n/a | domain guards; `Recompute` never reaches them for a thin window (it chooses `PROVISIONAL`) |
| anything else | `internal-error` 500 | retry, then dead-letter |
