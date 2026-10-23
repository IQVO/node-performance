-- scorecards: the NodeScorecard aggregate, one row per (node, window).
--   scorecard_id   nodeId + ':' + window (the aggregate root id).
--   scorecard_window  '7d' | '30d' ("window" is a reserved word).
--   grade          A | B | C | D, NULL while PROVISIONAL.
--   grade_label    the wire grade: the grade when GRADED, else PROVISIONAL
--                  (generated, so the by-grade query reads one indexed column).
--   dirty          observations were recorded since the last recompute.
--   expires_at     the instant the oldest observation leaves the window;
--                  NULL when the scorecard holds none. Together with dirty it
--                  decides which scorecards the recompute scheduler takes.
--   version        optimistic concurrency: every command increments it; a
--                  save expects the stored version to be version - 1.
-- Ids order byte-wise (COLLATE "C") in every query, like the in-memory doubles.
CREATE TABLE scorecards (
    scorecard_id                TEXT             PRIMARY KEY,
    node_id                     TEXT             NOT NULL,
    scorecard_window            TEXT             NOT NULL CHECK (scorecard_window IN ('7d', '30d')),
    state                       TEXT             NOT NULL CHECK (state IN ('PROVISIONAL', 'GRADED')),
    grade                       TEXT             CHECK (grade IN ('A', 'B', 'C', 'D')),
    grade_label                 TEXT             GENERATED ALWAYS AS (CASE WHEN state = 'GRADED' THEN grade ELSE 'PROVISIONAL' END) STORED,
    on_time_handover_rate       DOUBLE PRECISION NOT NULL CHECK (on_time_handover_rate BETWEEN 0 AND 1),
    late_ack_rate               DOUBLE PRECISION NOT NULL CHECK (late_ack_rate BETWEEN 0 AND 1),
    reject_rate                 DOUBLE PRECISION NOT NULL CHECK (reject_rate BETWEEN 0 AND 1),
    expiry_rate                 DOUBLE PRECISION NOT NULL CHECK (expiry_rate BETWEEN 0 AND 1),
    promise_kept_rate           DOUBLE PRECISION NOT NULL CHECK (promise_kept_rate BETWEEN 0 AND 1),
    feed_freshness_p95_minutes  INTEGER          NOT NULL CHECK (feed_freshness_p95_minutes >= 0),
    observations                INTEGER          NOT NULL CHECK (observations >= 0),
    computed_at                 TIMESTAMPTZ,
    dirty                       BOOLEAN          NOT NULL,
    expires_at                  TIMESTAMPTZ,
    version                     BIGINT           NOT NULL CHECK (version >= 1),
    CONSTRAINT scorecards_id_key CHECK (scorecard_id = node_id || ':' || scorecard_window),
    CONSTRAINT scorecards_grade_state CHECK ((state = 'GRADED') = (grade IS NOT NULL)),
    CONSTRAINT scorecards_graded_minimum CHECK (state <> 'GRADED' OR observations >= 50)
);

CREATE INDEX idx_scorecards_node ON scorecards (node_id);
CREATE INDEX idx_scorecards_grade ON scorecards (grade_label, (scorecard_id COLLATE "C"));
CREATE INDEX idx_scorecards_dirty ON scorecards (scorecard_id) WHERE dirty;
CREATE INDEX idx_scorecards_expires ON scorecards (expires_at) WHERE expires_at IS NOT NULL;

-- scorecard_observations: the observations a scorecard counts, keyed by the
-- CloudEvents id of the source event (an observation counts once). A save
-- replaces the set; rows that left the window are deleted by it.
CREATE TABLE scorecard_observations (
    scorecard_id    TEXT        NOT NULL REFERENCES scorecards (scorecard_id) ON DELETE CASCADE,
    source_event_id TEXT        NOT NULL,
    kind            TEXT        NOT NULL CHECK (kind IN ('ACK_ACCEPTED', 'ACK_REJECTED', 'ACK_EXPIRED', 'HANDOVER', 'PROMISE_KEPT', 'PROMISE_BROKEN', 'FEED_APPLIED')),
    observed_at     TIMESTAMPTZ NOT NULL,
    late            BOOLEAN     NOT NULL DEFAULT false,
    on_time         BOOLEAN     NOT NULL DEFAULT false,
    PRIMARY KEY (scorecard_id, source_event_id)
);

CREATE INDEX idx_scorecard_observations_at ON scorecard_observations (scorecard_id, observed_at);

-- purchase_order_facts: the local read model of what vendor-connectivity said
-- about a purchase order (ADR 0003, decision 3). An instant is only ever
-- filled, never overwritten or erased.
CREATE TABLE purchase_order_facts (
    po_number        TEXT        PRIMARY KEY,
    node_id          TEXT        NOT NULL,
    issued_at        TIMESTAMPTZ,
    ack_by           TIMESTAMPTZ,
    required_ship_by TIMESTAMPTZ
);
