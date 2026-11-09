package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
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
			done, err := r.one(ctx, id)
			events := done.events
			r.recordOutcome(ctx, events, err)
			switch {
			case errors.Is(err, ports.ErrConcurrentUpdate):
				res.Conflicts++
			case err != nil:
				return res, fmt.Errorf("recompute %s: %w", id, err)
			case events >= 0:
				res.Recomputed++
				res.Events += events
				r.logFlip(ctx, done.flip)
			}
		}
		if len(ids) < RecomputeBatch {
			break
		}
	}
	return res, ctx.Err()
}

// recomputed is what one scorecard's recompute did: how many events it raised
// (-1: the scorecard vanished or was no longer due) and the eligibility flip it
// caused, when it did.
type recomputed struct {
	events int
	flip   *scorecard.NodeTierEligibilityChanged
}

// one recomputes one scorecard in one unit of work.
func (r Recompute) one(ctx context.Context, id string) (recomputed, error) {
	done := recomputed{events: -1}
	err := transact(ctx, r.UoW, func(ctx context.Context) error {
		done = recomputed{events: -1}
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
		done = recomputed{events: len(events), flip: firstTierChange(events)}
		return nil
	})
	return done, err
}

// firstTierChange returns the first NodeTierEligibilityChanged of events: the
// two events of one flip carry the same facts, so it stands for the flip.
func firstTierChange(events []shared.Event) *scorecard.NodeTierEligibilityChanged {
	for _, e := range events {
		if c, ok := e.(scorecard.NodeTierEligibilityChanged); ok {
			return &c
		}
	}
	return nil
}

// logFlip writes the INFO line of one fast-tier eligibility flip, after the
// unit of work committed.
func (r Recompute) logFlip(ctx context.Context, flip *scorecard.NodeTierEligibilityChanged) {
	if flip == nil {
		return
	}
	r.logger().InfoContext(ctx, "fast tier eligibility changed",
		"node_id", string(flip.NodeID), "eligible", flip.Eligible, "metric", string(flip.Metric),
		"value", flip.Value, "threshold", flip.Threshold, "window", string(flip.Window))
}

// internalErrorReason is the reason of every recompute failure that is not a
// lost concurrency race (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one scorecard recompute: recomputed when it ran, failed
// with the reason otherwise. A scorecard that vanished or was no longer due
// (events < 0, no error) is not an attempt.
func (r Recompute) recordOutcome(ctx context.Context, events int, err error) {
	switch {
	case errors.Is(err, ports.ErrConcurrentUpdate):
		r.metrics().ScorecardRecomputeFailed(ctx, "concurrent-update")
	case err != nil:
		r.metrics().ScorecardRecomputeFailed(ctx, internalErrorReason)
	case events >= 0:
		r.metrics().ScorecardRecomputed(ctx)
	}
}
