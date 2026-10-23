package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/application/outbox"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// OutboxRepo is the Postgres transactional outbox, write side
// (ports.EventOutbox). Called with a UnitOfWork ctx it inserts on that
// transaction, so the rows land (or vanish) atomically with the aggregates;
// outside a unit of work the batch is its own transaction. The relay's read
// side (claiming and marking rows published) is RelayStore.
type OutboxRepo struct {
	pool    *pgxpool.Pool
	encoder outbox.Encoder
}

var _ ports.EventOutbox = (*OutboxRepo)(nil)

// ErrNilEncoder is returned by Append when the repository was built without an
// encoder; it is a wiring error, never a client error.
var ErrNilEncoder = errors.New("outbox repository has no encoder")

// NewOutboxRepo constructs an OutboxRepo over pool; encoder turns each domain
// event into the message that is stored.
func NewOutboxRepo(pool *pgxpool.Pool, encoder outbox.Encoder) *OutboxRepo {
	return &OutboxRepo{pool: pool, encoder: encoder}
}

type headerDoc struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func marshalHeaders(hs []outbox.Header) ([]byte, error) {
	out := make([]headerDoc, len(hs))
	for i, h := range hs {
		out[i] = headerDoc{Key: h.Key, Value: h.Value}
	}
	return json.Marshal(out)
}

const insertOutbox = `INSERT INTO outbox_events (event_id, topic, event_type, subject, key, dataschema, value, headers)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// Append implements ports.EventOutbox: it encodes every event first (so an
// encoding failure writes nothing) and then stores one unpublished row per
// event, in order.
func (r *OutboxRepo) Append(ctx context.Context, events ...shared.Event) error {
	if len(events) == 0 {
		return nil
	}
	if r.encoder == nil {
		return ErrNilEncoder
	}
	type row struct {
		msg     outbox.Message
		headers []byte
	}
	rows := make([]row, len(events))
	for i, ev := range events {
		msg, err := r.encoder.Encode(ev)
		if err != nil {
			return fmt.Errorf("encode %s: %w", ev.EventName(), err)
		}
		headers, err := marshalHeaders(msg.Headers)
		if err != nil {
			return fmt.Errorf("marshal outbox headers for %s: %w", msg.EventType, err)
		}
		rows[i] = row{msg: msg, headers: headers}
	}
	return atomically(ctx, r.pool, func(tx pgx.Tx) error {
		for _, rw := range rows {
			m := rw.msg
			if _, err := tx.Exec(ctx, insertOutbox, m.EventID, m.Topic, m.EventType, m.Subject, m.Key, m.DataSchema, m.Value, rw.headers); err != nil {
				return fmt.Errorf("enqueue outbox event %s: %w", m.EventType, err)
			}
		}
		return nil
	})
}
