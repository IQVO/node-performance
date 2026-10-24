package features_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/application/usecases"
)

// bddNow is the service clock at the start of every scenario.
var bddNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// purchaseOrder is what a scenario told the service about one purchase order.
type purchaseOrder struct {
	node      string
	issuedAt  time.Time
	ackWindow time.Duration
	shipBy    time.Duration
}

// world is the state of one scenario: the in-memory store behind the use
// cases, the purchase orders the scenario issued, and the outcome of the last
// command.
type world struct {
	store     *memory.Store
	clock     *memory.Clock
	intake    usecases.Intake
	recompute usecases.Recompute
	queries   usecases.Queries

	orders map[string]purchaseOrder
	seq    int

	lastOutcome  usecases.Outcome
	lastErr      error
	outboxBefore int
}

func newWorld() *world {
	w := &world{}
	w.reset()
	return w
}

// reset wires a fresh empty store and clock.
func (w *world) reset() {
	s := memory.NewStore()
	w.store = s
	w.clock = memory.NewClock(bddNow)
	deps := usecases.Deps{
		Scorecards: s.Scorecards(), Facts: s.Facts(), Inbound: s.Inbound(), Inbox: s.Inbox(), Outbox: s.Outbox(),
		UoW: s.UnitOfWork(), Clock: w.clock,
	}
	w.intake, w.recompute, w.queries = usecases.NewIntake(deps), usecases.NewRecompute(deps), usecases.Queries{Reader: s.Reader(), Inbound: s.Inbound(), Clock: w.clock}
	w.orders, w.seq = map[string]purchaseOrder{}, 0
	w.lastOutcome, w.lastErr, w.outboxBefore = "", nil, 0
}

func (w *world) ctx() context.Context { return context.Background() }

// nextID returns a fresh CloudEvents id.
func (w *world) nextID(prefix string) string {
	w.seq++
	return fmt.Sprintf("%s-%d", prefix, w.seq)
}

// ref builds the envelope of a consumed event happening now.
func (w *world) ref(id string) usecases.EventRef {
	return usecases.EventRef{ID: id, Type: "com.example.Event", Time: w.clock.Now()}
}

// run executes one command and remembers its outcome and where the outbox
// stood, so the Then steps can ask what the command published. A failed
// command is not a failed step: the scenario asserts the outcome.
func (w *world) run(command func() (usecases.Outcome, error)) {
	w.outboxBefore = len(w.store.Outbox().Events())
	w.lastOutcome, w.lastErr = command()
}

// given executes a precondition: it must succeed, or the scenario is broken.
func (w *world) given(what string, command func() (usecases.Outcome, error)) error {
	if _, err := command(); err != nil {
		return fmt.Errorf("precondition %q failed: %w", what, err)
	}
	return nil
}

// published returns "subject EventName" of every event the last command appended.
func (w *world) published() []string {
	events := w.store.Outbox().Events()[w.outboxBefore:]
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.SubjectID() + " " + e.EventName()
	}
	return out
}

// errorNamed maps the name a scenario uses to the error it stands for.
func errorNamed(name string) (error, error) {
	switch name {
	case "purchase order not known yet":
		return usecases.ErrPurchaseOrderUnknown, nil
	case "invalid event":
		return usecases.ErrInvalidEvent, nil
	default:
		return nil, errors.New("the scenario names an unknown error: " + name)
	}
}
