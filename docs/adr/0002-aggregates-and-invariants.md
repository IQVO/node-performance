# ADR 0002: the NodeScorecard aggregate, its invariants and the assumptions the model left open

## Status

Accepted (2026-10-23). Design source: retail-network-docs `model/model.yaml`,
context `node-performance` (aggregates, policies, `publishes`, `consumes`), the
pack pages `aggregate-design-canvas` and `domain-events`, the Model Gap Register
(G20, G33) and the orchestrator's `contracts-delta` (E18).

## Context

The model gives one aggregate root, `NodeScorecard`, identified by
`scorecardId = nodeId + ':' + window`; every node has two, the 7-day (`7d`) and
the 30-day (`30d`) scorecard. Its commands are `RecordObservation` and
`Recompute`; it raises `NodeScorecardUpdated` and `NodeThresholdBreached`. The
model is silent on most of what a scorecard needs to be computable: metric
definitions, denominators, the late-ack window, freshness sampling, the direction
of "downward", thresholds, creation, and which `PromiseBroken` causes count. G33
fixes `minObservations = 50`, "the first observation creates a PROVISIONAL card,
and recompute below the minimum stays PROVISIONAL without publishing", and the
node-attributable causes; G20/E18 fix that PROVISIONAL adds no handicap and that
consumers use the 30d window. Every other detail is an **assumption** below,
flagged `A<n>`, and is also reported to the orchestrator as a model gap. The model
is not edited here.

## Decision

### Layout and conventions

- `internal/domain/{shared,scorecard}`. Pure Go: no I/O, `time.Now` and id
  generation are never called; `now` is passed in. `scorecard` depends on
  `shared` only.
- `shared` holds `ErrValidation` (problem `validation-failed`, 422),
  `ErrIllegalTransition` (problem `illegal-transition`, 409), `ErrInvalidSnapshot`
  (a persisted row that breaks an invariant: an internal error), the `Event`
  interface and the identifier checks. `ErrNotFound` is the port sentinel
  `ports.ErrNotFound`; `ErrConcurrentUpdate` is raised by the repository on a
  version mismatch. Neither is a domain error.
- The aggregate has `New`, command methods, `Snapshot()` and a validating
  `Restore(Snapshot)`, and a `Version()` for optimistic concurrency. Every
  command that changes it increments the version; the repository stores with the
  expectation `stored version == Version()-1` (`0` for a create).
- Bounds that exist for Postgres limits are plain literals
  (`MaxInt32 = 2147483647`).

### NodeScorecard

States `PROVISIONAL` and `GRADED`. Transitions (model): `PROVISIONAL -> GRADED`,
`GRADED -> GRADED`, `GRADED -> PROVISIONAL`, all by `Recompute`. Creation is the
first `RecordObservation` for the node (A1); `PROVISIONAL -> PROVISIONAL` is a
recompute that stays below the minimum (A5). There is no terminal state.

| Invariant | Err | Enforced by |
| --- | --- | --- |
| rates are in [0,1] | `ErrRateOutOfRange` | `NewMetrics` (also rejects NaN, a negative freshness or a negative observation count) |
| a grade requires `observations >= minObservations` | `ErrInsufficientObservations` | `GradeFor(metrics)`: refuses to grade a thin window. `Recompute` branches on the count first and moves the scorecard to `PROVISIONAL` instead (A10) |
| an observation is counted once (idempotent on source event id) | `ErrDuplicateObservation` | `NodeScorecard.RecordObservation`; the consumer treats it as success (A20) |

Other validation errors wrap `ErrValidation`: an invalid node id or window, an
observation without a source event id or an instant, an unknown observation kind.

### Policies

`Grading` (model): `A` when `onTimeHandoverRate >= 0.98 and promiseKeptRate >= 0.97
and lateAckRate <= 0.01`; `B` at `0.95 / 0.94 / 0.03`; `C` at `0.90 / 0.90 / 0.05`;
otherwise `D`. `ThresholdAlert` (model): publish `NodeThresholdBreached` when a
metric crosses its threshold downward (A11).

### Assumptions (the model is silent)

- **A1. Aggregate shape and creation.** A `NodeScorecard` holds the observations
  counted in its window, keyed by the source CloudEvents `id`. `RecordObservation`
  is one use case that records ONE observation on BOTH scorecards of the node in
  one transaction; the first observation for a node creates both, `PROVISIONAL`,
  version 1 (G33). `RecordObservation` raises no event.
- **A2. Which consumed events are observations.** The observations are the
  measured outcomes: `ACK_ACCEPTED` (PurchaseOrderAccepted), `ACK_REJECTED`
  (PurchaseOrderRejected), `ACK_EXPIRED` (PurchaseOrderExpired), `HANDOVER`
  (PackageHandedOver), `PROMISE_KEPT` (PromiseKept), `PROMISE_BROKEN`
  (PromiseBroken, node-attributable causes only, A8) and `FEED_APPLIED`
  (InventoryFeedApplied). `PurchaseOrderIssued` and `VendorShipmentConfirmed` are
  facts about a purchase order, not outcomes: they upsert the local
  `PurchaseOrderFacts` (ADR 0003) and are not counted. `observations` in
  `Metrics` is therefore the number of measured outcomes in the window, so
  `minObservations = 50` means fifty outcomes, not fifty messages. The model's
  RecordObservation wording for the two fact events is narrowed on purpose: a
  purchase order that was issued and never resolved is not evidence yet.
- **A3. Observation instant and window.** The instant `At` of an observation is
  the fact's own instant: `acceptedAt`, `rejectedAt`, the expired order's `ackBy`,
  `handedOverAt`, `deliveredAt` (a broken promise without `deliveredAt` uses the
  envelope `time`) and `appliedAt`. An observation counts in a window at `now`
  when `At` is after `now - window` (a future instant counts: clocks skew).
  `Recompute` drops observations that are no longer inside the window.
- **A4. Events consumed without effect.** `VendorOnboarded`,
  `SubmissionProcessed`, `NodeRegistered` and `NodeOperationalProfileDeclared`
  are subscribed to and acknowledged, and only their inbox row is written. The
  model gives them no use here: the first two carry no `nodeId`, and a scorecard
  is computed from observed facts, never from the declared profile (business
  decision 1). They stay subscribed so a later decision (a node directory, the
  declared-versus-measured view) needs no new consumer group. Open question.
- **A5. Metrics, general rules.** Each metric is a numerator over a denominator
  with a better-direction (below). A metric with an empty denominator reports its
  best value (rate `1` for the higher-is-better rates, `0` for the rest). A
  `PROVISIONAL` scorecard still carries the metrics of what was observed (the REST
  read shows them), but its `grade` is `PROVISIONAL`. `minObservations` is
  `MinObservations = 50` (G33).
- **A6. onTimeHandoverRate** (higher is better) = `HANDOVER` observations that are
  on time / `HANDOVER` observations. On time means `handedOverAt` is NOT after
  the purchase order's `requiredShipBy` (the boundary instant is on time). The
  vendor's own `shippedAt` is never used.
- **A7. Acknowledgement rates** (lower is better). The acknowledged-or-expired
  population is the `ACK_ACCEPTED`, `ACK_REJECTED` and `ACK_EXPIRED` observations.
  `lateAckRate` = (late acknowledgements + expired orders) / that population; an
  acknowledgement is late when it falls in the LAST 10% of the ack window
  `[issuedAt, ackBy]` (`ackedAt >= ackBy - (ackBy - issuedAt)/10`, so an
  acknowledgement at or after `ackBy` is late); an expired order is always late.
  `issuedAt` is the CloudEvents `time` of `PurchaseOrderIssued` (the instant the
  order was issued). `rejectRate` = `ACK_REJECTED` / that population; `expiryRate` =
  `ACK_EXPIRED` / that population. An acceptance or rejection for a purchase order
  whose issue was not seen cannot be judged: the use case fails with
  `ErrPurchaseOrderUnknown`, which the consumer treats as transient (retried, then
  dead-lettered, ADR 0003).
- **A8. promiseKeptRate** (higher is better) = `PROMISE_KEPT` / (`PROMISE_KEPT` +
  `PROMISE_BROKEN`). G33 names the node-attributable causes `LATE_HANDOVER`,
  `VENDOR_REJECT` and `ACK_EXPIRED`, but delivery-promise's published
  `PromiseBroken.cause` is `LATE`, `LOST` or `RETURNED` (its `apis/asyncapi.yaml`).
  Reading: `LATE` (delivered after `deliverByLatest`, a node's handover is the
  usual reason) counts against the node together with the three G33 literals, in
  case a producer sends them; `LOST` and `RETURNED` (network and shopper) and any
  unknown cause do not: no observation is recorded and the inbox row is the only
  trace. **Model gap:** the G33 names and the delivery-promise vocabulary differ;
  the orchestrator should align them.
- **A9. feedFreshnessP95Minutes** (lower is better). The samples are the gaps
  between consecutive `FEED_APPLIED` observations of the node inside the window,
  ordered by `At`; each gap is truncated to whole minutes. The value is the 95th
  percentile by the nearest-rank method (`ceil(0.95 * n)`-th smallest); fewer than
  two feeds give no sample and `0` (A5). The trailing gap to `now` is not a sample,
  so a recompute without new facts never changes it.
- **A10. Grade assignment.** `GradeFor(metrics)` applies `Grading` and refuses with
  `ErrInsufficientObservations` below `MinObservations`. `Recompute` computes the
  metrics, then: below the minimum the state is `PROVISIONAL` and the grade is
  empty (on the wire `PROVISIONAL`), else the state is `GRADED` with the grade of
  `GradeFor`. All comparisons use the unrounded rates; a rate is a ratio of two
  integers, so `49/50` equals the literal `0.98`.
- **A11. Alert thresholds and "downward".** The model names no threshold per
  metric. The thresholds are the `C` bounds for the three graded metrics (the
  edge of a `D`) and these defaults for the rest, one table in the code:
  `onTimeHandoverRate >= 0.90`, `promiseKeptRate >= 0.90`, `lateAckRate <= 0.05`,
  `rejectRate <= 0.10`, `expiryRate <= 0.05`, `feedFreshnessP95Minutes <= 240`.
  "Downward" is a worsening: a higher-is-better metric that moves from `>=` the
  threshold to below it, or a lower-is-better metric that moves from `<=` the
  threshold to above it, between the scorecard's previous GRADED computation and
  the new GRADED one. No alert without both sides (a first graded recompute, or
  one coming from `PROVISIONAL`, has no baseline). One `NodeThresholdBreached` per
  metric that crossed; `value` is the new value, `threshold` the bound.
- **A12. Wire types.** Rates are numbers in [0,1], unrounded; `feedFreshnessP95Minutes`
  and `observations` are integers (durations and counts are integers on the wire);
  `computedAt` is the recompute instant, RFC 3339 UTC; `grade` is `A`, `B`, `C`,
  `D` or `PROVISIONAL`. The `NodeScorecardUpdated` `data` is the model list exactly.
- **A13. When a recompute publishes.** A recompute saves the scorecard (new
  `computedAt`, version) every time. It raises `NodeScorecardUpdated` when the
  state, the grade or any metric differs from the previous computation, except
  `PROVISIONAL -> PROVISIONAL`, which never publishes (G33). `GRADED -> PROVISIONAL`
  does publish (grade `PROVISIONAL`): consumers must not keep acting on a grade the
  window no longer supports. `NodeThresholdBreached` only goes with an update.
  Both events carry the recompute instant (`computedAt`, CloudEvents `time`).
- **A14. Window slide.** Observations that left the window are dropped from the
  scorecard at `Recompute`; a scorecard whose window emptied is `PROVISIONAL` with
  best-value metrics and `observations = 0`.
- **A15. Recompute trigger.** A single `Recompute` use case, driven by a scheduler
  in `cmd/api` every `RECOMPUTE_INTERVAL` (default one minute), recomputes every
  scorecard that is due: it has observations recorded since its last recompute
  (`dirty`) or its oldest observation has left the window (`expires_at <= now`).
  Several replicas are safe: the version check makes the loser skip the scorecard.
- **A16. Ids.** `nodeId` is a node-topology id (`shared.ValidID`, 1 to 64 characters
  of `A-Za-z0-9._:-`) and must not contain `:`; the `scorecardId` is
  `nodeId:window`, at most 68 characters. Source event ids are 1 to 200 characters
  without control characters.
- **A17. Concurrency.** Two consumers can record observations for the same node at
  once; the second save fails with `ports.ErrConcurrentUpdate` and the whole attempt
  (inbox row, load, record, save) runs again on fresh state, up to five times.
- **A18. REST additions.** `GET /scorecards?grade=` takes the optional filters
  `window`, `limit` (1 to 500, default 100) and `after` (keyset on `scorecardId`);
  the response has `nextCursor` when more rows exist. `GET /scorecards/{nodeId}`
  answers `not-found` until the first observation. The only problem types of this
  context are `not-found` and `validation-failed`: the three invariants are
  consumer-side.
- **A19. Bounds.** `observations` is bounded by `MaxInt32 = 2147483647` (a Postgres
  `integer`). A node id is at most 64 characters.
- **A20. Duplicates.** A redelivered event is stopped by the inbox (`processed_events`)
  first. `ErrDuplicateObservation` is the aggregate's second line when the same
  source id arrives through a path the inbox did not see; the consumer treats it
  as success.
- **A21. Idempotency keys.** Both REST routes are reads, so no route takes an
  `Idempotency-Key`. The HTTP adapter still ships the fleet middleware and the
  `idempotency_keys` table (decision D3) for any future POST/PUT; nothing uses them
  today.
- **A22. Load shape.** A recorded observation loads, and a save rewrites the
  observation set of, the two scorecards of one node (the 30d card holds about a
  month of outcomes of ONE node). That is acceptable for the "medium" throughput of
  the model; a compacted counter representation is the follow-up if a node's volume
  grows by orders of magnitude.
- **A23. Streams.** `NodeThresholdBreached` is analytics-only: it goes ONLY to
  `retailnetwork.node-performance.analytics` with the dataschema stream `analytics`;
  `NodeScorecardUpdated` goes ONLY to `.events` (contracts-delta shared
  conventions). The model pack put both on `.events`; the cross-context rule wins.

## Consequences

- delivery-promise and order-sourcing read the 30d `NodeScorecardUpdated`; a
  `PROVISIONAL` grade adds no handicap (G20, E18).
- `model_gaps` for the orchestrator: A2 (observation set), A4 (events with no use),
  A7 (the 10% rule needs `issuedAt`), A8 (cause vocabulary), A9 (freshness
  sampling), A11 (thresholds), A13 (publish rule), A15 (recompute scheduler), A18
  (REST additions), A23 (analytics stream).
- Adding a metric changes `Metrics`, `NewMetrics`, the OpenAPI schema and the
  `NodeScorecardUpdated` message together (additive evolution).
