//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
	"github.com/claudioed/node-performance/internal/application/outbox"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

const eventsTopic = "retailnetwork.node-performance.events"

var errEncode = errors.New("encode failed")

// testEncoder is a stand-in for the production encoder (built with the relay):
// a real CloudEvents envelope around the event's own fields.
type testEncoder struct{ failOn string }

func (e testEncoder) Encode(ev shared.Event) (outbox.Message, error) {
	if ev.EventName() == e.failOn {
		return outbox.Message{}, errEncode
	}
	id := uuid.NewString()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(),
		Time: ev.OccurredAt(), Stream: cloudevents.StreamEvents, Version: 1, Data: ev,
	})
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		EventID: id, Topic: eventsTopic, EventType: cloudevents.Type(ev.Entity(), ev.EventName()),
		Subject: ev.SubjectID(), Key: []byte(ev.SubjectID()),
		DataSchema: cloudevents.DataSchema(cloudevents.StreamEvents, ev.EventName(), 1), Value: value,
		Headers: []outbox.Header{{Key: "content-type", Value: cloudevents.MediaType}},
	}, nil
}

// fixtureOver wires every Postgres adapter over pool.
func fixtureOver(pool *pgxpool.Pool, enc outbox.Encoder) repocontract.Fixture {
	return repocontract.Fixture{
		Scorecards: postgres.NewScorecardRepo(pool), Reader: postgres.NewReader(pool),
		Facts: postgres.NewPurchaseOrderFactsRepo(pool), Inbox: postgres.NewInboxRepo(pool),
		Outbox: postgres.NewOutboxRepo(pool, enc), UoW: postgres.NewUnitOfWork(pool),
		OutboxCount: func(ctx context.Context) (int, error) {
			var n int
			err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n)
			return n, err
		},
	}
}

// newFixture returns the adapters over a fresh migrated database.
func newFixture(t *testing.T) (repocontract.Fixture, *pgxpool.Pool) {
	t.Helper()
	pool := newPool(t)
	return fixtureOver(pool, testEncoder{}), pool
}

// count returns the number of rows of table (a constant of the tests).
func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// noErr fails the test on err.
func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
