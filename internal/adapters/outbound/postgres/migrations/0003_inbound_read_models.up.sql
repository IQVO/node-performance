-- Local read models of the inbound metrics (ADR 0007).
--
-- inbound_shipments: the shipped instant of each shipment, from seller-inbound's
-- InboundShipmentShipped. The first fact stored wins (ON CONFLICT DO NOTHING).
CREATE TABLE inbound_shipments (
    shipment_id TEXT        PRIMARY KEY,
    node_id     TEXT        NOT NULL,
    shipped_at  TIMESTAMPTZ NOT NULL
);

-- inbound_receipts: one row per shipment receipt, from vendor-connectivity's
-- InboundReceiptSubmitted, reduced to the carton counts. The shipment_id key
-- makes a second report of the same shipment a no-op, so a receipt is counted
-- once. A receipt whose shipment has not arrived yet is parked here: the
-- metrics read joins it the moment inbound_shipments has the row.
CREATE TABLE inbound_receipts (
    shipment_id      TEXT        PRIMARY KEY,
    node_id          TEXT        NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL,
    cartons_total    INTEGER     NOT NULL CHECK (cartons_total >= 0),
    cartons_received INTEGER     NOT NULL CHECK (cartons_received >= 0 AND cartons_received <= cartons_total)
);

-- The metrics read: receipts of some nodes inside a window.
CREATE INDEX idx_inbound_receipts_node_received ON inbound_receipts (node_id, received_at);
