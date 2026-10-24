package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

var errBoom = errors.New("boom")

// base is the fixed clock of every test.
var base = repocontract.Now

func at(minutes int) time.Time { return repocontract.At(minutes) }

// env is the use cases wired over one in-memory store.
type env struct {
	store     *memory.Store
	clock     *memory.Clock
	intake    usecases.Intake
	recompute usecases.Recompute
	queries   usecases.Queries
	deps      usecases.Deps
}

// newEnv wires every use case over the memory doubles; tweak, when given,
// replaces ports before the use cases are built.
func newEnv(tweak ...func(*usecases.Deps)) env {
	s := memory.NewStore()
	clock := memory.NewClock(base)
	d := usecases.Deps{
		Scorecards: s.Scorecards(), Facts: s.Facts(), Inbound: s.Inbound(), Inbox: s.Inbox(), Outbox: s.Outbox(),
		UoW: s.UnitOfWork(), Clock: clock,
	}
	for _, f := range tweak {
		f(&d)
	}
	return env{
		store: s, clock: clock, deps: d, intake: usecases.NewIntake(d), recompute: usecases.NewRecompute(d),
		queries: usecases.Queries{Reader: s.Reader(), Inbound: s.Inbound(), Clock: clock},
	}
}

func ref(id string, minutes int) usecases.EventRef {
	return usecases.EventRef{ID: id, Type: "com.example.Event", Time: at(minutes)}
}

// issue applies a PurchaseOrderIssued: ack window 12:00 to 22:00 (issued at
// base, ackBy +600 minutes), requiredShipBy +1800 minutes.
func (e env) issue(t *testing.T, po, node string) {
	t.Helper()
	out, err := e.intake.PurchaseOrderIssued(context.Background(), usecases.PurchaseOrderIssued{
		Ref: ref("issue-"+po, 0), PoNumber: po, NodeID: node, AckBy: at(600), RequiredShipBy: at(1800),
	})
	if err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("issue %s: %v, %v", po, out, err)
	}
}

func (e env) scorecard(t *testing.T, id string) *scorecard.NodeScorecard {
	t.Helper()
	s, err := e.store.Scorecards().Get(context.Background(), id)
	if err != nil {
		t.Fatalf("scorecard %s: %v", id, err)
	}
	return s
}

func (e env) noScorecard(t *testing.T, id string) {
	t.Helper()
	if _, err := e.store.Scorecards().Get(context.Background(), id); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("scorecard %s must not exist: %v", id, err)
	}
}

// observation returns the only observation of kind in the scorecard.
func observationOf(t *testing.T, s *scorecard.NodeScorecard, id string) scorecard.Observation {
	t.Helper()
	for _, o := range s.Snapshot().Observations {
		if o.SourceEventID == id {
			return o
		}
	}
	t.Fatalf("scorecard %s holds no observation %q", s.ID(), id)
	return scorecard.Observation{}
}

func wantOutcome(t *testing.T, got usecases.Outcome, err error, want usecases.Outcome) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("outcome = %q, %v; want %q", got, err, want)
	}
}

func wantCounts(t *testing.T, e env, want memory.Counts) {
	t.Helper()
	if got := e.store.Counts(); got != want {
		t.Fatalf("store counts = %+v, want %+v", got, want)
	}
}

// failingScorecards fails Save with err for the first n calls, then delegates.
type failingScorecards struct {
	ports.ScorecardRepository
	err   error
	n     int
	calls int
}

func (f *failingScorecards) Save(ctx context.Context, s *scorecard.NodeScorecard) error {
	f.calls++
	if f.calls <= f.n {
		return f.err
	}
	return f.ScorecardRepository.Save(ctx, s)
}
