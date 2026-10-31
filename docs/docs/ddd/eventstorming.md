---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 6
description: "Process-level EventStorming of Node Performance: the timeline from node facts to a grade, with commands, policies, read models and hotspots."
---

# EventStorming

Following the ddd-crew
[EventStorming glossary](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet): domain
events in orange, commands in blue, policies in lilac, read models in green, external systems in pink
and hotspots marked. The board is reconstructed from the code, not from a workshop.

## Timeline

```mermaid
flowchart LR
    subgraph facts["Facts arrive (external systems)"]
        e1["PurchaseOrderIssued / Accepted / Rejected / Expired"]
        e2["PackageHandedOver"]
        e3["PromiseKept / PromiseBroken"]
        e4["InventoryFeedApplied"]
        e5["InboundShipmentShipped / InboundReceiptSubmitted"]
    end
    p1{{"Policy: deduplicate on the CloudEvents id"}}
    c1["Command: record observation"]
    a1(("NodeScorecard"))
    ev1["Observation recorded, scorecard due"]
    p2{{"Policy: every RECOMPUTE_INTERVAL, recompute what is due"}}
    c2["Command: recompute"]
    p3{{"Policy: Grading and ThresholdAlert"}}
    ev2["NodeScorecardUpdated"]
    ev3["NodeThresholdBreached"]
    p4{{"Policy: delivery-promise handicap"}}
    p5{{"Policy: order-sourcing assignment cost"}}
    rm1[/"Read model: scorecards by node, by grade"/]
    e1 --> p1
    e2 --> p1
    e3 --> p1
    e4 --> p1
    e5 --> p1
    p1 --> c1
    c1 --> a1
    a1 --> ev1
    ev1 --> p2
    p2 --> c2
    c2 --> a1
    a1 --> p3
    p3 --> ev2
    p3 --> ev3
    ev2 --> p4
    ev2 --> p5
    a1 --> rm1
```

Source: `internal/adapters/inbound/kafka/handlers.go` (the facts), `internal/application/usecases/intake.go`
(deduplication and recording), `cmd/api/workers.go` (`runRecompute`),
`internal/domain/scorecard/scorecard.go` and `grade.go` (grading and alert policies),
`internal/adapters/outbound/kafka/encoder.go` (the two events).

## Hotspots

| Hotspot | What the code does about it |
| --- | --- |
| Facts arrive in no global order across topics | A handover or acknowledgement that beats its purchase order is retried (200 ms doubling to 5 s, 5 attempts) and then dead-lettered; there is no automatic redrive from the DLQ. |
| `PromiseBroken` for causes that are not the node's fault | Only `LATE`, `LATE_HANDOVER`, `VENDOR_REJECT`, `ACK_EXPIRED` count; other causes are acknowledged and ignored. |
| A quiet new node must not look good | Under 50 observations a scorecard is `PROVISIONAL` and publishes nothing. |
| Observations leave the window with no new fact | `ExpiresAt` makes a scorecard due when its oldest observation expires, so a grade can fall back to `PROVISIONAL`. |
| Declared profile next to measured capability | Not modelled: `NodeOperationalProfileDeclared` is consumed with no effect and no read model holds it. |
| Inbound accuracy mixes node and seller causes | Informational only, never graded. |
| Kafka down while recomputing | The event waits in the outbox; delivery is at-least-once, consumers deduplicate on the `id`. |

## Aggregate and read model

One aggregate, `NodeScorecard` ([canvas](/docs/ddd/aggregate-design-canvas)). The two reads
(`GET /scorecards/{nodeId}`, `GET /scorecards?grade=`) read its table directly; there is no separate
read model.

## Policies

| Policy | When | Then | Owner |
| --- | --- | --- | --- |
| Deduplicate | an event arrives | claim its CloudEvents id in `processed_events`, or stop | Node Performance |
| Late acknowledgement | an acknowledgement is recorded | late if in the last tenth of the ack window | Node Performance |
| Grading | a recompute has 50 or more observations | grade `A` to `D` | Node Performance |
| Threshold alert | a recompute moves a metric across its bound | raise `NodeThresholdBreached` | Node Performance |
| Scorecard handicap | `NodeScorecardUpdated` | delay the node's ship-by | `delivery-promise` |
| Assignment cost | `NodeScorecardUpdated` | weigh the grade in the cost | `order-sourcing` |
