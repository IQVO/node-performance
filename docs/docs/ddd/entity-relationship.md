---
id: entity-relationship
title: Entity relationship
sidebar_label: Entity relationship
sidebar_position: 8
description: "The Postgres schema of Node Performance: the scorecard aggregate tables, the local fact tables, the inbox, the outbox and the idempotency table."
---

# Entity relationship

The schema of this context's own Postgres database, created by three embedded migrations
(`internal/adapters/outbound/postgres/migrations/0001` to `0003`). A relationship line is drawn only
where a foreign key exists, and foreign keys exist only inside one aggregate.

```mermaid
erDiagram
    scorecards ||--o{ scorecard_observations : "FK scorecard_id, ON DELETE CASCADE"
    scorecards {
        TEXT scorecard_id PK "nodeId:window"
        TEXT node_id
        TEXT scorecard_window "CHECK 7d, 30d"
        TEXT state "CHECK PROVISIONAL, GRADED"
        TEXT grade "nullable, A to D, set only when GRADED"
        TEXT grade_label "generated: grade when GRADED else PROVISIONAL"
        DOUBLE on_time_handover_rate "CHECK 0 to 1"
        DOUBLE late_ack_rate "CHECK 0 to 1"
        DOUBLE reject_rate "CHECK 0 to 1"
        DOUBLE expiry_rate "CHECK 0 to 1"
        DOUBLE promise_kept_rate "CHECK 0 to 1"
        INTEGER feed_freshness_p95_minutes "CHECK at least 0"
        INTEGER observations "CHECK at least 0, at least 50 when GRADED"
        TIMESTAMPTZ computed_at "nullable until the first recompute"
        BOOLEAN dirty "observations since the last recompute"
        TIMESTAMPTZ expires_at "when the oldest observation leaves the window"
        BIGINT version "optimistic concurrency"
    }
    scorecard_observations {
        TEXT scorecard_id PK,FK
        TEXT source_event_id PK "CloudEvents id of the source fact"
        TEXT kind "ACK_ACCEPTED ... FEED_APPLIED"
        TIMESTAMPTZ observed_at "the fact's own instant"
        BOOLEAN late
        BOOLEAN on_time
    }
```

Source: `internal/adapters/outbound/postgres/migrations/0001_node_performance_schema.up.sql`,
`scorecard_repository.go`, `reader.go`.

```mermaid
erDiagram
    purchase_order_facts {
        TEXT po_number PK
        TEXT node_id
        TIMESTAMPTZ issued_at "nullable, only ever filled"
        TIMESTAMPTZ ack_by "nullable"
        TIMESTAMPTZ required_ship_by "nullable"
    }
    inbound_shipments {
        TEXT shipment_id PK
        TEXT node_id
        TIMESTAMPTZ shipped_at "first fact wins"
    }
    inbound_receipts {
        TEXT shipment_id PK "joined to inbound_shipments by value"
        TEXT node_id
        TIMESTAMPTZ received_at
        INTEGER cartons_total
        INTEGER cartons_received "at most cartons_total"
    }
```

Source: `0001_node_performance_schema.up.sql` (`purchase_order_facts`),
`0003_inbound_read_models.up.sql`, `facts_repository.go`, `inbound_repository.go`. No foreign key joins
`inbound_receipts` to `inbound_shipments`: a receipt may arrive before its shipment and is parked.

```mermaid
erDiagram
    outbox_events {
        BIGSERIAL id PK
        TEXT event_id "CloudEvents id, UNIQUE with topic"
        TEXT topic "events or analytics topic"
        TEXT event_type "full CloudEvents type"
        TEXT subject "scorecardId"
        BYTEA key "Kafka key"
        TEXT dataschema
        BYTEA value "structured-mode CloudEvents JSON"
        JSONB headers "content-type, traceparent"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ published_at "nullable"
        INTEGER attempts
        TEXT last_error
    }
    processed_events {
        TEXT event_id PK "CloudEvents id"
        TEXT event_type
        TIMESTAMPTZ processed_at
    }
    idempotency_keys {
        TEXT route PK
        TEXT key PK
        TEXT request_hash
        INTEGER response_status
        BYTEA response_body
        TIMESTAMPTZ created_at
    }
    schema_migrations {
        BIGINT version PK
        BOOLEAN dirty
    }
```

Source: `0002_outbox_inbox_and_idempotency.up.sql`, `outbox_repository.go`, `relay_store.go`,
`inbox_repository.go`, `migrate.go` (golang-migrate creates `schema_migrations`).

## Tables and aggregates

| Table | Aggregate / role | Notes |
| --- | --- | --- |
| `scorecards` | **NodeScorecard** (root) | One row per `(node_id, window)`; `version` guards concurrent saves; `grade_label` is a stored generated column read by the by-grade query. |
| `scorecard_observations` | **NodeScorecard** (its observations) | Primary key `(scorecard_id, source_event_id)` enforces `ErrDuplicateObservation` at the database too; a save replaces the set and deletes the rows that left the window. |
| `purchase_order_facts` | local copy | Facts judged against by acknowledgements and handovers; an instant is only ever filled, never overwritten or erased. |
| `inbound_shipments`, `inbound_receipts` | local copies | Joined at read time for the informational inbound metrics. |
| `processed_events` | infrastructure | The consumer inbox, idempotent on the CloudEvents `id`, written in the same transaction as the effect. |
| `outbox_events` | infrastructure | One row per event, written in the same transaction as the scorecard; drained by the relay. |
| `idempotency_keys` | infrastructure | Reserved for a future write route; rows would live 24 hours. No route uses it. |
| `schema_migrations` | infrastructure | golang-migrate bookkeeping. |

## Indexes

`idx_scorecards_node (node_id)` for `GET /scorecards/{nodeId}`; `idx_scorecards_grade (grade_label,
scorecard_id COLLATE "C")` for the by-grade keyset page; partial `idx_scorecards_dirty` (`WHERE dirty`)
and `idx_scorecards_expires` (`WHERE expires_at IS NOT NULL`) for the recompute scheduler;
`idx_scorecard_observations_at (scorecard_id, observed_at)`; partial `idx_outbox_events_unpublished
(id) WHERE published_at IS NULL`; `idx_inbound_receipts_node_received`; `idx_idempotency_keys_created_at`.

## Why no foreign key leaves the aggregate

- `scorecards.node_id` names a node owned by `node-topology`, in another database. It is a value copied
  from events, not a reference: facts about a node must be accepted before (or without) its
  `NodeRegistered`, because Kafka gives no cross-topic order.
- `purchase_order_facts.po_number` and the shipment ids are other contexts' ids; a foreign key would
  couple this schema to theirs.
- The two scorecards of one node are two aggregates sharing no foreign key; both are written in one
  transaction because the same fact feeds them.
