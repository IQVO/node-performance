# 1. Node Performance is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: retail-network-docs `model/model.yaml`, context `node-performance`, and its ADRs RN-0001 and RN-0007.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. Measures every node against what it declared and what was asked of it: acknowledgement latency, rejections, on-time handover against requiredShipBy, promise-kept rate and inventory feed freshness, over rolling windows, and grades the node.

## Decision

- This repo owns the aggregates NodeScorecard; the invariants and error names are those of the model.
- Business decisions of the context:
- A scorecard is computed only from observed facts, never from the vendor's declaration.
- A node with fewer than the minimum observations in the window is graded PROVISIONAL, not A.
- Grades drive the promise handicap (owned by delivery-promise); this context never suspends a node.
- Events use the network CloudEvents namespace `com.retailnetwork.partner.node-performance.<entity>.<Event>` on `retailnetwork.node-performance.events`
  (analytics-only events on `.analytics`). Published: NodeScorecardUpdated, NodeThresholdBreached. No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.

## Addendum 2026-11-13: current state

Two statements above are out of date. Since ADR 0013 the context does suspend a node from the `SAME_DAY` and `NEXT_DAY` tiers (not its status in `node-topology`): the `FastTierEligibility` policy runs in `Recompute` of the 7d scorecard (`internal/domain/scorecard/eligibility.go:91`, `scorecard.go:173`). And three events are published, not two: `NodeTierEligibilityChanged` (topic `retailnetwork.node-performance.events`, consumed by `delivery-promise`) joins `NodeScorecardUpdated` and `NodeThresholdBreached` (`events.go`, `internal/adapters/outbound/kafka/encoder.go:131`).
