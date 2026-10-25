package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// InboundRepo is the Postgres ports.InboundRepository and ports.InboundReader.
type InboundRepo struct{ pool *pgxpool.Pool }

var (
	_ ports.InboundRepository = (*InboundRepo)(nil)
	_ ports.InboundReader     = (*InboundRepo)(nil)
)

// NewInboundRepo constructs the repository over pool.
func NewInboundRepo(pool *pgxpool.Pool) *InboundRepo { return &InboundRepo{pool: pool} }

// SaveShipment implements ports.InboundRepository: the first instant wins.
func (r *InboundRepo) SaveShipment(ctx context.Context, s scorecard.InboundShipment) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO inbound_shipments (shipment_id, node_id, shipped_at)
		VALUES ($1, $2, $3) ON CONFLICT (shipment_id) DO NOTHING`, s.ShipmentID, string(s.NodeID), s.ShippedAt)
	if err != nil {
		return false, fmt.Errorf("save inbound shipment %s: %w", s.ShipmentID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveReceipt implements ports.InboundRepository: the first receipt wins.
func (r *InboundRepo) SaveReceipt(ctx context.Context, rc scorecard.InboundReceipt) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO inbound_receipts (shipment_id, node_id, received_at, cartons_total, cartons_received)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (shipment_id) DO NOTHING`,
		rc.ShipmentID, string(rc.NodeID), rc.ReceivedAt, rc.CartonsTotal, rc.CartonsReceived)
	if err != nil {
		return false, fmt.Errorf("save inbound receipt %s: %w", rc.ShipmentID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Observations implements ports.InboundReader: the INNER join leaves parked
// receipts out until their shipment arrives.
func (r *InboundRepo) Observations(ctx context.Context, nodes []scorecard.NodeID, after time.Time) (map[scorecard.NodeID][]scorecard.InboundObservation, error) {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = string(n)
	}
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT r.node_id, r.shipment_id, r.received_at, r.cartons_total, r.cartons_received, s.shipped_at
		FROM inbound_receipts r JOIN inbound_shipments s ON s.shipment_id = r.shipment_id
		WHERE r.node_id = ANY($1) AND r.received_at > $2
		ORDER BY r.received_at, r.shipment_id`, ids, after)
	if err != nil {
		return nil, fmt.Errorf("read inbound observations: %w", err)
	}
	defer rows.Close()
	out := map[scorecard.NodeID][]scorecard.InboundObservation{}
	for rows.Next() {
		var (
			node, shipment      string
			received, shipped   time.Time
			cartons, cartonsGot int
		)
		if err := rows.Scan(&node, &shipment, &received, &cartons, &cartonsGot, &shipped); err != nil {
			return nil, fmt.Errorf("scan inbound observation: %w", err)
		}
		rc := scorecard.InboundReceipt{
			ShipmentID: shipment, NodeID: scorecard.NodeID(node), ReceivedAt: shared.Instant(received),
			CartonsTotal: cartons, CartonsReceived: cartonsGot,
		}
		out[rc.NodeID] = append(out[rc.NodeID], scorecard.JoinInbound(rc, shared.Instant(shipped)))
	}
	return out, rows.Err()
}
