---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 9
description: "UML sequence diagrams of observation intake, recompute and the outbox relay in Node Performance, with idempotency and version-check branches."
---

# Sequence diagrams

One diagram per use case. `UoW` is `ports.UnitOfWork`, one Postgres transaction; `Inbox` is the
`processed_events` table and `Outbox` the `outbox_events` table, both written inside the same
transaction as the aggregate. The relay drains the outbox to Kafka outside that transaction. Use-case
prose is on [Use cases](/docs/ddd/use-cases).

## 1. Intake of an observation (Kafka consumer)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant KC as kafka<br/>consumer
    participant UC as Intake
    participant U as UoW,<br/>Inbox
    participant F as Facts<br/>repository
    participant R as Scorecard<br/>repository
    participant A as NodeScorecard
    KC->>KC: decode CloudEvent,<br/>dispatch on full type
    KC->>UC: PackageHandedOver(ref, data)
    UC->>U: begin, MarkProcessed(id)
    alt id already processed
        U-->>UC: not fresh
        UC-->>KC: duplicate, commit offset
    else first delivery
        UC->>F: Get(poNumber)
        alt requiredShipBy unknown
            F-->>UC: not found
            UC->>U: rollback
            UC-->>KC: purchase order not known yet,<br/>retry then dead-letter
        else facts known
            F-->>UC: PurchaseOrderFacts
            UC->>UC: NewHandover(on time?)
            loop window 7d and 30d
                UC->>R: Get(nodeId:window)
                alt scorecard missing
                    R-->>UC: not found
                    UC->>A: Open(first observation)
                else found
                    R-->>UC: scorecard, version v
                    UC->>A: RecordObservation
                end
                UC->>R: Save, expects version v
            end
            alt lost the race
                R-->>UC: concurrent update
                UC->>U: rollback
                UC->>UC: rerun the unit, up to 5 times
            else saved
                UC->>U: commit
                UC-->>KC: applied, commit offset
            end
        end
    end
```

Source: `internal/application/usecases/intake.go` (`apply`, `PackageHandedOver`, `record`, `recordIn`),
`writer.go` (`transact`), `internal/adapters/inbound/kafka/kafka.go` (`run`, `handleWithDeadLetter`).
No outbox row is written: recording an observation raises no event. The dead-letter path is on the
[Runbook](/docs/operations/runbook#dead-letter-queues).

## 2. Recompute (scheduler)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant T as scheduler
    participant UC as Recompute
    participant U as UoW
    participant R as Scorecard<br/>repository
    participant A as NodeScorecard
    participant OB as Outbox
    T->>UC: Run(ctx)
    UC->>R: ListDue(now, 200)
    R-->>UC: scorecard ids
    loop each due scorecard
        UC->>U: begin
        UC->>R: Get(id)
        R-->>UC: scorecard, version v
        alt vanished or no longer due
            UC->>U: commit, no attempt
        else due
            UC->>A: Recompute(now)
            A->>A: drop observations<br/>left the window
            A->>A: Compute metrics
            alt fewer than 50 observations
                A->>A: PROVISIONAL
            else enough
                A->>A: GRADED, grade A to D
            end
            A-->>UC: events, if changed
            UC->>R: Save, expects version v
            alt lost the race
                R-->>UC: concurrent update
                UC->>U: rollback
                Note over UC: counted concurrent-update,<br/>stays due for the next run
            else saved
                UC->>OB: Append(events)
                UC->>U: commit
                Note over UC: counted recomputed
            end
        end
    end
```

Source: `internal/application/usecases/recompute.go` (`Run`, `one`, `recordOutcome`),
`internal/domain/scorecard/scorecard.go` (`Recompute`, `eventsSince`), `cmd/api/workers.go`
(`runRecompute`). Any other error ends the run, is logged `recompute round failed`, counted
`internal-error` and retried at the next tick.

## 3. Relay to Kafka

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant RL as outbox relay
    participant OB as Outbox<br/>store
    participant K as Kafka
    loop every OUTBOX_RELAY_INTERVAL
        RL->>OB: Drain(limit, maxAttempts),<br/>unpublished rows in id order, skip locked
        OB-->>RL: next row
        RL->>K: publish to the events<br/>or the analytics topic, key scorecardId
        alt broker ack
            RL->>OB: set published_at
        else send failed
            RL->>OB: attempts + 1, last_error
            Note over RL: stop this pass,<br/>a row at the cap is skipped and logged once
        end
    end
```

Source: `internal/adapters/outbound/outbox/relay.go` (`RelayOnce`, `Run`),
`internal/adapters/outbound/postgres/relay_store.go`, `internal/adapters/outbound/kafka/relay_sink.go`.
A crash after the broker ack and before `published_at` is set republishes the same CloudEvents `id`;
consumers deduplicate on it.

## 4. Read of a scorecard

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant C as client
    participant H as http handler
    participant Q as Queries
    participant RD as ScorecardReader
    participant IB as InboundReader
    C->>H: GET /scorecards/nodeId
    H->>Q: GetScorecard(nodeId)
    Q->>RD: ByNode(nodeId)
    alt none
        RD-->>Q: empty
        Q-->>H: not found
        H-->>C: 404 not-found
    else found
        RD-->>Q: 7d and 30d views
        Q->>IB: Observations(nodes, now minus 30d)
        IB-->>Q: joined receipts
        Q->>Q: add inbound metrics per window
        Q-->>H: NodeScorecards
        H-->>C: 200 JSON
    end
```

Source: `internal/application/usecases/queries.go`, `internal/adapters/inbound/http/handlers.go`.
`GET /scorecards?grade=` follows the same shape through `ScorecardReader.ByGrade`, with
`422 validation-failed` for a bad `grade` or `window` (REST: `400 invalid-limit`, `invalid-cursor` or
`unknown-query-parameter` for the paging parameters). MCP and A2A call the same
two use cases.
