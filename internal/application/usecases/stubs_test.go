package usecases_test

import (
	"context"
	"time"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// failingInbox fails every MarkProcessed.
type failingInbox struct{ err error }

func (f failingInbox) MarkProcessed(context.Context, string, string, time.Time) (bool, error) {
	return false, f.err
}

// failingFacts fails Get and Upsert with err.
type failingFacts struct{ err error }

func (f failingFacts) Get(context.Context, string) (scorecard.PurchaseOrderFacts, error) {
	return scorecard.PurchaseOrderFacts{}, f.err
}

func (f failingFacts) Upsert(context.Context, scorecard.PurchaseOrderFacts) error { return f.err }

// failingGet fails Get with err and delegates the rest.
type failingGet struct {
	ports.ScorecardRepository
	err error
}

func (f failingGet) Get(context.Context, string) (*scorecard.NodeScorecard, error) { return nil, f.err }

// failingList fails ListDue.
type failingList struct {
	ports.ScorecardRepository
	err error
}

func (f failingList) ListDue(context.Context, time.Time, int) ([]string, error) { return nil, f.err }

// fixedDue answers ListDue with ids whatever the state.
type fixedDue struct {
	ports.ScorecardRepository
	ids []string
}

func (f fixedDue) ListDue(context.Context, time.Time, int) ([]string, error) { return f.ids, nil }

// failingOutbox fails every Append.
type failingOutbox struct{ err error }

func (f failingOutbox) Append(context.Context, ...shared.Event) error { return f.err }
