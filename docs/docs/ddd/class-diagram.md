---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
sidebar_position: 7
description: "UML class diagrams of the NodeScorecard domain model, the application ports and the hexagonal adapters of Node Performance."
---

# Class diagram

UML class diagrams of the Go domain model in `internal/domain/scorecard`, followed by the ports and the
hexagonal adapters. Fields are shown unexported (`-`), methods exported (`+`); only state-changing
methods and key queries are shown.

## 1. NodeScorecard aggregate

### 1a. Root, observations and metrics

```mermaid
classDiagram
    direction TB
    class NodeScorecard {
        <<AggregateRoot>>
        -nodeID NodeID
        -window Window
        -state State
        -grade Grade
        -metrics Metrics
        -computedAt time.Time
        -dirty bool
        -version int64
        -observations map
        +Open(nodeID, window, first)$ NodeScorecard
        +Restore(snapshot)$ NodeScorecard
        +RecordObservation(o Observation) error
        +Recompute(now time.Time) Event[]
        +DueForRecompute(now time.Time) bool
        +ExpiresAt() time.Time
        +Snapshot() Snapshot
    }
    class Observation {
        <<Value>>
        +SourceEventID string
        +Kind Kind
        +At time.Time
        +Late bool
        +OnTime bool
    }
    class Metrics {
        <<ValueObject>>
        +OnTimeHandoverRate float64
        +LateAckRate float64
        +RejectRate float64
        +ExpiryRate float64
        +PromiseKeptRate float64
        +FeedFreshnessP95Minutes int
        +Observations int
        +NewMetrics(m)$ Metrics
        +Compute(observations)$ Metrics
    }
    NodeScorecard "1" *-- "0..*" Observation : observations
    NodeScorecard *-- Metrics
```

### 1b. Identity, enumerations and facts

```mermaid
classDiagram
    direction LR
    class NodeScorecard {
        <<AggregateRoot>>
    }
    class NodeID {
        <<ValueObject>>
        NewNodeID(s)
    }
    class Window {
        <<Enumeration>>
        7d
        30d
    }
    class State {
        <<Enumeration>>
        PROVISIONAL
        GRADED
    }
    class Grade {
        <<Enumeration>>
        A
        B
        C
        D
    }
    class Kind {
        <<Enumeration>>
        ACK_ACCEPTED
        ACK_REJECTED
        ACK_EXPIRED
        HANDOVER
        PROMISE_KEPT
        PROMISE_BROKEN
        FEED_APPLIED
    }
    class PurchaseOrderFacts {
        <<ValueObject>>
        +PoNumber string
        +NodeID NodeID
        +IssuedAt time.Time
        +AckBy time.Time
        +RequiredShipBy time.Time
        +Merge(newer) PurchaseOrderFacts
        +CanJudgeAck() bool
        +CanJudgeHandover() bool
    }
    class InboundShipment {
        <<ValueObject>>
        +ShipmentID string
        +ShippedAt time.Time
    }
    class InboundReceipt {
        <<ValueObject>>
        +ShipmentID string
        +ReceivedAt time.Time
    }
    NodeScorecard --> NodeID
    NodeScorecard --> Window
    NodeScorecard --> State
    NodeScorecard --> Grade : 0..1
    NodeScorecard ..> Kind : observations
    InboundReceipt ..> InboundShipment : joined by shipmentId
```

Source: `internal/domain/scorecard/scorecard.go`, `observation.go`, `metrics.go`, `grade.go`,
`window.go`, `purchaseorder.go`, `inbound.go`. The errors are `ErrRateOutOfRange`,
`ErrInsufficientObservations` and `ErrDuplicateObservation` (`window.go`). `PROVISIONAL` is a `State`;
the wire grade is the string `PROVISIONAL` and the `Grade` field is empty.

## 2. Events, policies and ports

### 2a. Domain events

```mermaid
classDiagram
    direction LR
    class NodeScorecard {
        <<AggregateRoot>>
    }
    class NodeScorecardUpdated {
        <<DomainEvent>>
        +NodeID NodeID
        +Window Window
        +State State
        +Grade Grade
        +Metrics Metrics
        +ComputedAt time.Time
        +AnalyticsOnly() false
    }
    class NodeThresholdBreached {
        <<DomainEvent>>
        +NodeID NodeID
        +Window Window
        +Metric MetricName
        +Value float64
        +Threshold float64
        +AnalyticsOnly() true
    }
    NodeScorecard ..> NodeScorecardUpdated : raises
    NodeScorecard ..> NodeThresholdBreached : raises
```

Source: `internal/domain/scorecard/events.go`.

### 2b. Policies and application ports

```mermaid
classDiagram
    direction TB
    class NodeScorecard {
        <<AggregateRoot>>
    }
    class GradingBars {
        <<Policy>>
        +GradeFor(m Metrics) Grade
    }
    class Thresholds {
        <<Policy>>
        +Breaches(prev, next Metrics) Breach[]
    }
    class ScorecardRepository {
        <<Port>>
        +Get(ctx, id) NodeScorecard
        +Save(ctx, s) error
        +ListDue(ctx, now, limit) string[]
    }
    class ScorecardReader {
        <<Port>>
        +ByNode(ctx, nodeID) Scorecard[]
        +ByGrade(ctx, filter) ScorecardPage
    }
    class PurchaseOrderFactsRepository {
        <<Port>>
        +Get(ctx, po) PurchaseOrderFacts
        +Upsert(ctx, facts) error
    }
    class ProcessedEvents {
        <<Port>>
        +MarkProcessed(ctx, id, type, at) bool
    }
    class EventOutbox {
        <<Port>>
        +Append(ctx, events) error
    }
    class UnitOfWork {
        <<Port>>
        +Do(ctx, fn) error
    }
    NodeScorecard ..> GradingBars : Recompute
    NodeScorecard ..> Thresholds : Recompute
    ScorecardRepository ..> NodeScorecard : persists
```

Source: `internal/application/ports/ports.go`, `internal/domain/scorecard/grade.go`. Also in `ports`:
`Clock`, `InboundRepository`, `InboundReader` and `ScorecardMetrics`.

## 3. Hexagonal ports and adapters

```mermaid
flowchart TB
    subgraph IN["internal/adapters/inbound"]
        HTTP["http<br/>GET /scorecards/nodeId<br/>GET /scorecards?grade="]
        KC["kafka<br/>six consumers"]
        MCP["mcp<br/>two read tools"]
        A2A["a2a<br/>two read skills"]
    end
    TICK["cmd/api workers.go<br/>recompute scheduler"]
    subgraph APP["internal/application"]
        INT["usecases.Intake"]
        REC["usecases.Recompute"]
        QRY["usecases.Queries"]
        PORTS["ports"]
    end
    DOM["internal/domain/scorecard"]
    subgraph OUT["internal/adapters/outbound"]
        PG["postgres<br/>repositories, outbox, inbox, unit of work"]
        ENC["kafka.Encoder"]
        RL["outbox relay + kafka.RelaySink"]
        TEL["telemetry<br/>ScorecardMetrics"]
    end
    KC --> INT
    TICK --> REC
    HTTP --> QRY
    MCP --> QRY
    A2A --> QRY
    INT --> DOM
    REC --> DOM
    INT --> PORTS
    REC --> PORTS
    QRY --> PORTS
    PORTS --- PG
    PG --> ENC
    PG --> RL
    REC --> TEL
```

Source: `cmd/api/run.go` (`postgresAdapters`, `buildServer`), `cmd/api/workers.go`,
`cmd/mcp/main.go` and `cmd/a2a/main.go` (`buildDeps`), `internal/architecture/architecture_test.go`.
There is no outbound HTTP, MCP or A2A client. The in-memory adapter and `repocontract` are test-only.
