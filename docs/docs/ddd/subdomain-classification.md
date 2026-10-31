---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
description: "Node Performance is a Supporting subdomain of the retail network: the reasoning, and the classification of its neighbours."
---

# Subdomain classification

**Verdict: Supporting**, partner tier (CloudEvents segment `partner`), business model *risk reducer*,
evolution *custom-built*. The classification comes from the retail network's model
(`network/docs/strategic-design/subdomain-classification.md` in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification));
the reasoning below is checked against this repository's code.

## Why Supporting

- **It measures, it does not decide.** The grade is an input to decisions taken elsewhere:
  `delivery-promise` applies the handicap to the promise, `order-sourcing` prices the assignment, and
  suspending a node is a `node-topology` command issued by people. Node Performance publishes
  `NodeScorecardUpdated` and stops (`internal/domain/scorecard` has no policy that changes another
  context's data).
- **The hard, competitive part is elsewhere.** The network's advantage is the delivery promise and the
  late assignment (the two Core contexts). Measured capability is what keeps *declared* capability
  honest, but customers do not choose a network for how it grades its vendors.
- **It is specific enough not to be Generic.** The metrics (on-time handover against
  `requiredShipBy`, acknowledgement in the last tenth of the ack window, node-attributable broken
  promises, feed freshness p95), the 50-observation minimum and the A to D bars are this network's own
  rules (`grade.go`, `metrics.go`), so it is custom-built rather than bought.
- **It is replaceable behind a stable contract.** The grading table and the alert table are small,
  declarative and unit-tested to 100% mutation efficacy; the published event is the only coupling.

## Where it sits in the network

| Bounded context | Classification | Tier | Relation to Node Performance |
| --- | --- | --- | --- |
| `delivery-promise` | Core | `commerce` | upstream for `PromiseKept` / `PromiseBroken`; downstream for `NodeScorecardUpdated` |
| `order-sourcing` | Core | `commerce` | downstream for `NodeScorecardUpdated` |
| `node-topology` | Supporting | `logistics` | upstream (`NodeRegistered`, `NodeOperationalProfileDeclared`, acknowledged without effect) |
| `offer-availability` | Supporting | `commerce` | upstream (`InventoryFeedApplied`) |
| `vendor-connectivity` | Supporting | `partner` | upstream (eight event types); calls `GET /scorecards/{nodeId}` |
| `seller-inbound` | Supporting | `partner` | upstream (`InboundShipmentShipped`) |
| `shipping-and-tracking` | Generic | `logistics` | upstream (`PackageHandedOver`) |
| `linehaul-transportation`, `last-mile-delivery`, `customer-ordering` | Supporting, Supporting, Generic | `logistics`, `logistics`, `commerce` | no relationship (Separate Ways) |

The network's own count is two Core contexts, seven Supporting and two Generic, plus three named
future contexts. The classification of a Core context is "would buying it, and conforming to the
vendor's model, cost us the reason customers choose us": only the promise and the late assignment pass
that test.

## Relation to the warehouse-systems fleet

This service belongs to the retail network, not the warehouse-systems fleet, and the fleet reaches the
network only through the `vendor-connectivity` Vendor API. The fleet's own classification is in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/strategic-design/subdomain-classification).
The decision record is [ADR 0001](/docs/adr/0001-node-performance-bounded-context).
