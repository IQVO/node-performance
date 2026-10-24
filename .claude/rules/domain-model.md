# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: warehouse-docs `network/model/model.yaml` (context `node-performance`,
Supporting, partner tier). Invariants, metric definitions and every assumption the
model left open are in `docs/adr/0002-aggregates-and-invariants.md` (assumptions
A1-A23); the consumed contracts and local facts are in
`docs/adr/0003-consumed-contracts-and-local-facts.md`.

## Ubiquitous Language (use these exact names)

- **Node** — a building of the network, owned by node-topology and referenced here
  only by `nodeId`. This context never stores a node.
- **NodeScorecard** — the aggregate: how one node performed over one rolling
  **window** (`7d` or `30d`). Identified by `scorecardId = nodeId + ":" + window`
  (`node-0f3a91:30d`); that id is also the CloudEvents `subject` and the Kafka key.
  A node has two, created together by its first observation.
- **Observation** — one measured fact, counted once by the CloudEvents id of the
  event that carried it (`source_event_id`). Kinds: `ACK_ACCEPTED`, `ACK_REJECTED`,
  `ACK_EXPIRED`, `HANDOVER`, `PROMISE_KEPT`, `PROMISE_BROKEN`, `FEED_APPLIED`.
- **Metrics** — `onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`,
  `promiseKeptRate`, `feedFreshnessP95Minutes`, `observations`. A ratio is a
  numerator over a denominator; an empty denominator reports the best value.
- **PROVISIONAL** — fewer than `MinObservations = 50` observations in the window:
  no grade letter, never published while it stays provisional. **GRADED** — 50 or
  more: grade `A`, `B`, `C` or `D` by the `Grading` policy. On the wire `grade` is
  `PROVISIONAL` for a provisional scorecard.
- **Threshold breach** — a metric moving to the unhealthy side of its threshold
  between two GRADED recomputes (`NodeThresholdBreached`, analytics only).
- **PurchaseOrderFacts** — a local read model keyed by `poNumber` (`nodeId`,
  `issuedAt`, `ackBy`, `requiredShipBy`), filled by vendor-connectivity events. It
  is a view of what a producer said, never an aggregate.

## Aggregates

- **NodeScorecard** (`internal/domain/scorecard`): `Open`, `RecordObservation`,
  `Recompute(now)`; states `PROVISIONAL` and `GRADED`; an observation id is counted
  once per scorecard (`ErrDuplicateObservation`); a rate stays in [0,1]
  (`ErrRateOutOfRange`); a grade needs `MinObservations` (`ErrInsufficientObservations`);
  observations older than the window are dropped by `Recompute`.
- `internal/domain/shared`: `Event`, `Header`, `ErrValidation`, `ErrIllegalTransition`,
  `ErrInvalidSnapshot`, id and text checks, `Instant` (UTC, microsecond precision,
  the precision Postgres stores).

`NodeScorecard` has `Snapshot()`, a validating `Restore(Snapshot)` and an optimistic
`Version()`; `now` is passed in. `ErrNotFound` and `ErrConcurrentUpdate` are port
errors (`internal/application/ports/errors.go`), not domain errors. The domain
imports nothing outside `internal/domain`.

## Domain events (`internal/domain/scorecard/events.go`; wire shape in `apis/asyncapi.yaml`)

- `NodeScorecardUpdated` — raised by `Recompute` when the state, grade or metrics
  of a GRADED scorecard changed, or when a GRADED scorecard fell back below the
  minimum. Integration event on `retailnetwork.node-performance.events`.
- `NodeThresholdBreached` — analytics only, on `retailnetwork.node-performance.analytics`.

## Use cases (`internal/application/usecases`)

Commands run in ONE `ports.UnitOfWork` (load, domain call, save, outbox append) and
retry on `ports.ErrConcurrentUpdate`:

- `Intake` (`intake.go`): one method per consumed event type (`PurchaseOrderIssued`,
  `PurchaseOrderAcknowledged`, `PurchaseOrderExpired`, `ShipmentConfirmed`,
  `PackageHandedOver`, `PromiseKept`, `PromiseBroken`, `InventoryFeedApplied`,
  `Acknowledge`). Each claims the CloudEvents id in the inbox in the same unit of work.
- `Recompute` (`recompute.go`): recomputes the due scorecards; run by the scheduler
  of `cmd/api` every `RECOMPUTE_INTERVAL`.
- `Queries` (`queries.go`): `GetScorecard`, `ListScorecardsByGrade`. Read DTOs are in
  `internal/application/views`; filters in `internal/application/repository`.
- Ports (`internal/application/ports/ports.go`) are interfaces only; the memory test
  doubles in `internal/adapters/outbound/memory` pass the same suite as Postgres
  (`internal/adapters/outbound/repocontract`).
