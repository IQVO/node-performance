// Package usecases holds the application's use cases: the intake of every
// consumed event (RecordObservation and the facts it needs), Recompute, and the
// two queries of apis/openapi.yaml. Every command follows one shape inside ONE
// ports.UnitOfWork: load, call the domain, save, append the raised events to
// the outbox. Domain, shared and port errors pass through unchanged (errors.Is
// keeps working): the HTTP adapter maps them to problem types and the Kafka
// consumers to retry or dead-letter.
package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/node-performance/internal/application/ports"
)

// maxAttempts bounds the retries of a unit of work that lost an optimistic
// concurrency race (ADR 0002, A17).
const maxAttempts = 5

// Deps bundles the ports the command use cases need.
type Deps struct {
	Scorecards ports.ScorecardRepository
	Facts      ports.PurchaseOrderFactsRepository
	Inbound    ports.InboundRepository
	Inbox      ports.ProcessedEvents
	Outbox     ports.EventOutbox
	UoW        ports.UnitOfWork
	Clock      ports.Clock
}

// writer is what every command embeds.
type writer struct{ Deps }

// now returns the clock's current time in UTC.
func (w writer) now() time.Time { return w.Clock.Now().UTC() }

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// (inbox row, load, domain call, save, outbox) runs again on fresh state, up to
// maxAttempts times, before the conflict is reported.
func transact(ctx context.Context, uow ports.UnitOfWork, attempt func(ctx context.Context) error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = uow.Do(ctx, attempt)
		if !errors.Is(err, ports.ErrConcurrentUpdate) {
			return err
		}
	}
	return err
}
