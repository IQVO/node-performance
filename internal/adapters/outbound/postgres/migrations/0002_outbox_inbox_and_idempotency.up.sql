-- outbox_events: the transactional outbox (same shape as the fleet siblings).
-- One row per already-encoded Kafka message, inserted in the SAME transaction
-- as the scorecard rows; the relay drains unpublished rows.
--   event_id    the CloudEvents `id`, minted once at append and persisted: a
--               relay retry republishes the same id.
--   topic       retailnetwork.node-performance.events | .analytics
--   event_type  the FULL CloudEvents `type`
--               (com.retailnetwork.partner.node-performance.scorecard.<EventName>).
--   subject     the CloudEvents `subject` (the scorecardId).
--   key         the Kafka message key (the scorecardId).
--   dataschema  the CloudEvents `dataschema` URN.
--   value       the structured-mode CloudEvents JSON bytes.
--   headers     [{"key":..,"value":..}] Kafka headers (content-type).
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- processed_events: the inbox of every Kafka consumer. The CloudEvents id is
-- claimed with INSERT ... ON CONFLICT DO NOTHING in the SAME transaction as the
-- effect, so a rollback un-claims it. Ids are UUIDs minted by the producers'
-- outboxes: unique across topics, so the id alone is the key.
CREATE TABLE processed_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL
);

-- idempotency_keys: the Idempotency-Key middleware of state-changing REST
-- routes. This context has none today (both routes are reads); the table is
-- the fleet standard for any future POST/PUT (ADR 0002, A21). A repeat of
-- (route, key) with the same request_hash replays the stored response; a
-- different hash is a conflict. Rows live 24 hours.
CREATE TABLE idempotency_keys (
    route           TEXT        NOT NULL,
    key             TEXT        NOT NULL,
    request_hash    TEXT        NOT NULL,
    response_status INTEGER,
    response_body   BYTEA,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (route, key)
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
