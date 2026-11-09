package scorecard

import (
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// midday is 12:00 UTC of the nth day after the fixed clock's day.
func midday(day int) time.Time { return base.AddDate(0, 0, day) }

// handovers returns n handovers, onTime or late, one minute apart from start.
func handovers(prefix string, n int, onTime bool, start time.Time) []Observation {
	out := many(prefix, KindHandover, n, start)
	for i := range out {
		out[i].OnTime = onTime
	}
	return out
}

// record adds the observations to s.
func record(t *testing.T, s *NodeScorecard, list []Observation) {
	t.Helper()
	for _, o := range list {
		if err := s.RecordObservation(o); err != nil {
			t.Fatalf("RecordObservation(%s): %v", o.SourceEventID, err)
		}
	}
}

// suspended7d returns a 7d scorecard recomputed on day 0 at 50 of 60
// handovers on time (0.833), so the node is suspended with no streak.
func suspended7d(t *testing.T) *NodeScorecard {
	t.Helper()
	list := append(handovers("ok", 50, true, midday(0).Add(-2*time.Hour)), handovers("late", 10, false, midday(0).Add(-time.Hour))...)
	s := open(t, Window7d, list...)
	events := s.Recompute(midday(0))
	if got := tierEvents(events); len(got) != 2 || s.FastTier().Eligible() {
		t.Fatalf("setup: suspended = %v, tier events = %d", !s.FastTier().Eligible(), len(got))
	}
	return s
}

// lift adds on-time handovers so the 7d rate climbs to about 0.97.
func lift(t *testing.T, s *NodeScorecard, at time.Time) {
	t.Helper()
	record(t, s, handovers(fmt.Sprintf("lift-%d", at.Unix()), 400, true, at.Add(-8*time.Hour)))
}

// tierEvents returns the NodeTierEligibilityChanged events of list.
func tierEvents(list []shared.Event) []NodeTierEligibilityChanged {
	var out []NodeTierEligibilityChanged
	for _, e := range list {
		if tier, ok := e.(NodeTierEligibilityChanged); ok {
			out = append(out, tier)
		}
	}
	return out
}

// wantStreak fails unless the node is suspended with the streak and its day.
func wantStreak(t *testing.T, s *NodeScorecard, streak, day int) {
	t.Helper()
	got := s.FastTier()
	want := time.Time{}
	if streak > 0 {
		want = utcDay(midday(day))
	}
	if !got.Suspended || got.Streak != streak || !got.StreakDay.Equal(want) {
		t.Fatalf("fast tier = %+v, want suspended, streak %d on %v", got, streak, want)
	}
}
