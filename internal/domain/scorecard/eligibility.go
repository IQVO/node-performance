package scorecard

import (
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// Tier is a fast delivery tier governed by the FastTierEligibility policy.
type Tier string

// The governed tiers. Both follow one eligibility flag per node.
const (
	TierSameDay Tier = "SAME_DAY"
	TierNextDay Tier = "NEXT_DAY"
)

// fastTiers is every governed tier, in event order.
var fastTiers = [...]Tier{TierSameDay, TierNextDay}

// FastTiers returns a copy of the governed tiers.
func FastTiers() []Tier { return append([]Tier(nil), fastTiers[:]...) }

// The FastTierEligibility policy (ADR 0013), in the style of gradingBars: the
// 7d on-time handover rate below suspendBelow suspends a node; the rate at or
// above reinstateAt on reinstateAfterDays consecutive distinct UTC days of
// recompute reinstates it. The gap between the two rates is the hysteresis.
const (
	suspendBelow       = 0.90
	reinstateAt        = 0.95
	reinstateAfterDays = 3
)

// FastTierState is the persisted state of the FastTierEligibility policy. It
// lives on the 7d scorecard; the zero value is ELIGIBLE with no streak, which
// is where every node starts.
type FastTierState struct {
	// Suspended is true while the node is ineligible for the fast tiers.
	Suspended bool
	// Streak is the number of distinct UTC days, in a row, on which a GRADED
	// recompute stood at or above the reinstatement rate; zero unless Suspended,
	// and always below reinstateAfterDays.
	Streak int
	// StreakDay is the last UTC day counted in Streak (UTC midnight); zero
	// while Streak is zero.
	StreakDay time.Time
}

// Eligible reports whether the node may win the fast tiers.
func (f FastTierState) Eligible() bool { return !f.Suspended }

// validate checks the invariants of a persisted state of a scorecard of window w.
func (f FastTierState) validate(w Window) error {
	switch {
	case f.Suspended && w != Window7d:
		return shared.Invalid("only the 7d scorecard can suspend a node from the fast tiers")
	case f.Streak < 0 || f.Streak >= reinstateAfterDays:
		return shared.Invalid("reinstatement streak must be 0 to %d, got %d", reinstateAfterDays-1, f.Streak)
	case !f.Suspended && f.Streak != 0:
		return shared.Invalid("an eligible node has no reinstatement streak")
	case (f.Streak == 0) != f.StreakDay.IsZero():
		return shared.Invalid("the reinstatement streak and its last day go together")
	}
	return nil
}

// normalised returns f with the streak day at UTC midnight.
func (f FastTierState) normalised() FastTierState {
	if !f.StreakDay.IsZero() {
		f.StreakDay = utcDay(f.StreakDay)
	}
	return f
}

// utcDay is the UTC calendar day of t, at midnight.
func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// tierFlip is the eligibility change a recompute caused.
type tierFlip struct {
	eligible  bool
	value     float64
	threshold float64
}

// assess applies the FastTierEligibility policy to one recompute of the 7d
// scorecard. Only a GRADED scorecard moves the state; a PROVISIONAL one leaves
// it, streak included, as it was. The flip is non-nil when the flag changed.
func (f FastTierState) assess(state State, rate float64, now time.Time) (FastTierState, *tierFlip) {
	if state != StateGraded {
		return f, nil
	}
	if !f.Suspended {
		if rate < suspendBelow {
			return FastTierState{Suspended: true}, &tierFlip{eligible: false, value: rate, threshold: suspendBelow}
		}
		return f, nil
	}
	if rate < reinstateAt {
		return FastTierState{Suspended: true}, nil
	}
	today := utcDay(now)
	if !today.After(f.StreakDay) {
		return f, nil // the same day twice counts once
	}
	if f.Streak+1 >= reinstateAfterDays {
		return FastTierState{}, &tierFlip{eligible: true, value: rate, threshold: reinstateAt}
	}
	return FastTierState{Suspended: true, Streak: f.Streak + 1, StreakDay: today}, nil
}

// tierEvents returns the events of one flip: one per tier, same instant.
func (s *NodeScorecard) tierEvents(flip *tierFlip) []shared.Event {
	if flip == nil {
		return nil
	}
	out := make([]shared.Event, 0, len(fastTiers))
	for _, tier := range fastTiers {
		out = append(out, NodeTierEligibilityChanged{
			Header: shared.Header{Version: s.version, At: s.computedAt}, NodeID: s.nodeID, Tier: tier,
			Eligible: flip.eligible, Metric: MetricOnTimeHandoverRate, Value: flip.value,
			Threshold: flip.threshold, Window: s.window, ChangedAt: s.computedAt,
		})
	}
	return out
}

// FastTier returns the FastTierEligibility state (the 7d scorecard holds it).
func (s *NodeScorecard) FastTier() FastTierState { return s.fastTier }
