package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/node-performance/internal/application/ports"
)

const (
	// RecomputeBatch is how many due scorecards one round loads.
	RecomputeBatch = 200
	// maxRecomputeRounds bounds one Run, so a scorecard that keeps failing
	// cannot hold the scheduler in a loop.
	maxRecomputeRounds = 10
)

// RecomputeResult is what one Run did.
type RecomputeResult struct {
	// Recomputed is the number of scorecards recomputed and saved.
	Recomputed int
	// Events is the number of events appended to the outbox.
	Events int
	// Conflicts is the number of scorecards skipped because another writer got
	// in between the load and the save more than maxAttempts times; they stay
	// due and the next Run takes them.
	Conflicts int
}

// Recompute is the Recompute command, run by the scheduler of cmd/api over the
// scorecards that are due (ADR 0002, A15).
type Recompute struct{ writer }

// NewRecompute builds the Recompute use case.
func NewRecompute(d Deps) Recompute { return Recompute{writer{d}} }

// Run recomputes every due scorecard, a batch at a time. A failure other than a
// lost concurrency race ends the Run and is returned with what was done.
func (r Recompute) Run(ctx context.Context) (RecomputeResult, error) {
	var res RecomputeResult
	for round := 0; round < maxRecomputeRounds; round++ {
		now := r.now()
		ids, err := r.Scorecards.ListDue(ctx, now, RecomputeBatch)
		if err != nil {
			return res, fmt.Errorf("list due scorecards: %w", err)
		}
		for _, id := range ids {
			events, err := r.one(ctx, id)
			switch {
			case errors.Is(err, ports.ErrConcurrentUpdate):
				res.Conflicts++
			case err != nil:
				return res, fmt.Errorf("recompute %s: %w", id, err)
			case events >= 0:
				res.Recomputed++
				res.Events += events
			}
		}
		if len(ids) < RecomputeBatch {
			break
		}
	}
	return res, ctx.Err()
}

// one recomputes one scorecard and returns how many events it raised; -1 means
// the scorecard vanished or was no longer due.
func (r Recompute) one(ctx context.Context, id string) (int, error) {
	raised := -1
	err := transact(ctx, r.UoW, func(ctx context.Context) error {
		raised = -1
		now := r.now()
		sc, err := r.Scorecards.Get(ctx, id)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !sc.DueForRecompute(now) {
			return nil
		}
		events := sc.Recompute(now)
		if err := r.Scorecards.Save(ctx, sc); err != nil {
			return err
		}
		if err := r.Outbox.Append(ctx, events...); err != nil {
			return fmt.Errorf("append events to outbox: %w", err)
		}
		raised = len(events)
		return nil
	})
	return raised, err
}
