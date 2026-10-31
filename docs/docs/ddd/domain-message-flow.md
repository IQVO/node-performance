---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
sidebar_position: 5
description: "Domain message flow modelling for Node Performance: the scenarios from a node's facts to a published grade, as commands, events and queries across contexts."
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Each scenario shows the messages that cross a context boundary (events on Kafka, the REST read) and
the policies inside Node Performance. Rounded boxes are contexts, square boxes are messages.

## Scenario 1: a node hands over a package

```mermaid
flowchart LR
    VC(["vendor-connectivity"])
    ST(["shipping-and-tracking"])
    NP(["node-performance"])
    DP(["delivery-promise"])
    OS(["order-sourcing"])
    VC -->|"PurchaseOrderIssued, VendorShipmentConfirmed"| NP
    ST -->|"PackageHandedOver"| NP
    NP -->|"NodeScorecardUpdated"| DP
    NP -->|"NodeScorecardUpdated"| OS
```

Source: `internal/application/usecases/intake.go` (`PurchaseOrderIssued`, `ShipmentConfirmed`,
`PackageHandedOver`), `internal/application/usecases/recompute.go`. The purchase-order facts must
arrive before the handover can be judged; a handover that beats them is retried, then dead-lettered.

## Scenario 2: a vendor acknowledges, rejects or lets an order expire

```mermaid
flowchart LR
    VC(["vendor-connectivity"])
    NP(["node-performance"])
    AN(["analytics"])
    VC -->|"PurchaseOrderIssued"| NP
    VC -->|"PurchaseOrderAccepted or Rejected"| NP
    VC -->|"PurchaseOrderExpired"| NP
    NP -->|"NodeScorecardUpdated"| DP(["delivery-promise, order-sourcing"])
    NP -->|"NodeThresholdBreached"| AN
```

Source: `internal/application/usecases/intake.go` (`PurchaseOrderAcknowledged`, `PurchaseOrderExpired`),
`internal/domain/scorecard/scorecard.go` (`eventsSince`).

## Scenario 3: promises are judged and feeds applied

```mermaid
flowchart LR
    DP(["delivery-promise"])
    OA(["offer-availability"])
    NP(["node-performance"])
    DP -->|"PromiseKept, PromiseBroken"| NP
    OA -->|"InventoryFeedApplied"| NP
    NP -->|"NodeScorecardUpdated"| DP
```

Source: `internal/adapters/inbound/kafka/handlers.go` (`DeliveryPromiseHandlers`,
`OfferAvailabilityHandlers`). `delivery-promise` is both upstream and downstream, by events only.

## Scenario 4: inbound timeliness and accuracy

```mermaid
flowchart LR
    SI(["seller-inbound"])
    VC(["vendor-connectivity"])
    NP(["node-performance"])
    VC2(["vendor-connectivity"])
    SI -->|"InboundShipmentShipped"| NP
    VC -->|"InboundReceiptSubmitted"| NP
    VC2 -->|"GET /scorecards/nodeId"| NP
```

Source: `internal/application/usecases/intake.go` (`InboundShipmentShipped`,
`InboundReceiptSubmitted`), `queries.go` (`addInbound`), `internal/domain/scorecard/inbound.go`.
Nothing is published for this scenario: the two metrics appear on the read only.

## Scenario 5: an operator, console or agent reads a grade

```mermaid
flowchart LR
    C(["clients, console, agents"])
    NP(["node-performance"])
    C -->|"GET /scorecards/nodeId, get_scorecard, node_performance"| NP
    C -->|"GET /scorecards?grade, list_scorecards_by_grade, nodes_by_grade"| NP
```

Source: `internal/adapters/inbound/http/handlers.go`, `internal/adapters/inbound/mcp/tools.go`,
`internal/adapters/inbound/a2a/skills.go`. All queries; no command exists on any interface.

## Message catalogue

| Message | Kind | Producer | Consumers |
| --- | --- | --- | --- |
| Facts (15 types) | events | the six upstream producers | Node Performance |
| `NodeScorecardUpdated` | event | Node Performance | `delivery-promise`, `order-sourcing` |
| `NodeThresholdBreached` | event | Node Performance | none |
| `GetScorecard` and `ListScorecardsByGrade` | queries | Node Performance (REST, MCP, A2A) | `vendor-connectivity`, agents, the console |

The full types, topics and fields are on the [Event catalogue](/docs/api-reference/events) and
[Domain events](/docs/ddd/domain-events).
