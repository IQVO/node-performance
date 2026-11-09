package scorecard

import (
	"testing"
	"time"
)

func TestFastTierEveryNodeStartsEligible(t *testing.T) {
	s := open(t, Window7d, handovers("h", 10, false, base)...)
	if !s.FastTier().Eligible() || s.FastTier() != (FastTierState{}) {
		t.Fatalf("a new scorecard is eligible with no streak: %+v", s.FastTier())
	}
	if events := s.Recompute(midday(0)); len(tierEvents(events)) != 0 {
		t.Fatalf("no event for the initial ELIGIBLE state: %+v", events)
	}
}

func TestFastTierSuspendsBelowNinetyPercentWithOneEventPerTier(t *testing.T) {
	list := append(handovers("ok", 50, true, base), handovers("late", 10, false, base.Add(time.Hour))...)
	s := open(t, Window7d, list...)
	events := s.Recompute(midday(0))
	tiers := tierEvents(events)
	if len(events) != 3 || len(tiers) != 2 {
		t.Fatalf("events = %+v, want the update then two tier events", events)
	}
	want := NodeTierEligibilityChanged{
		Header: tiers[0].Header, NodeID: "node-0f3a91", Eligible: false, Metric: MetricOnTimeHandoverRate,
		Value: 50.0 / 60.0, Threshold: 0.90, Window: Window7d, ChangedAt: midday(0),
	}
	for i, tier := range []Tier{TierSameDay, TierNextDay} {
		want.Tier = tier
		if tiers[i] != want {
			t.Errorf("tier event %d = %+v\nwant %+v", i, tiers[i], want)
		}
	}
	if tiers[0].Version != s.Version() || !tiers[0].OccurredAt().Equal(midday(0)) {
		t.Errorf("header = v%d %v, want v%d %v", tiers[0].Version, tiers[0].OccurredAt(), s.Version(), midday(0))
	}
	assertTierAccessors(t, tiers[0])
	if s.FastTier().Eligible() || s.FastTier().Streak != 0 {
		t.Errorf("the node is suspended: %+v", s.FastTier())
	}
}

func assertTierAccessors(t *testing.T, e NodeTierEligibilityChanged) {
	t.Helper()
	if e.EventName() != "NodeTierEligibilityChanged" || e.Entity() != "scorecard" || e.AnalyticsOnly() || e.SubjectID() != "node-0f3a91:7d" {
		t.Errorf("classification: %s %s analyticsOnly=%v subject=%s", e.EventName(), e.Entity(), e.AnalyticsOnly(), e.SubjectID())
	}
}

func TestFastTierExactlyNinetyPercentStaysEligible(t *testing.T) {
	list := append(handovers("ok", 90, true, base), handovers("late", 10, false, base.Add(time.Hour))...)
	s := open(t, Window7d, list...)
	if tiers := tierEvents(s.Recompute(midday(0))); len(tiers) != 0 || !s.FastTier().Eligible() {
		t.Fatalf("0.90 is the bar, not under it: %+v %+v", tiers, s.FastTier())
	}
}

func TestFastTierStaysEligibleWhileSuspendedStaysSuspendedOnce(t *testing.T) {
	s := suspended7d(t)
	if tiers := tierEvents(s.Recompute(midday(0).Add(time.Hour))); len(tiers) != 0 {
		t.Fatalf("a flag that did not flip raises nothing: %+v", tiers)
	}
	wantStreak(t, s, 0, 0)
}

func TestFastTierProvisionalNeverChangesEligibility(t *testing.T) {
	s := open(t, Window7d, handovers("late", 10, false, base)...)
	s.Recompute(midday(0))
	if !s.FastTier().Eligible() {
		t.Fatalf("a PROVISIONAL scorecard never suspends: %+v", s.FastTier())
	}
}

func TestFastTierProvisionalKeepsASuspensionAndItsStreak(t *testing.T) {
	s := suspended7d(t)
	lift(t, s, midday(1))
	s.Recompute(midday(1))
	wantStreak(t, s, 1, 1)
	// eight days later every observation left the window: PROVISIONAL
	events := s.Recompute(midday(9))
	if s.State() != StateProvisional || len(tierEvents(events)) != 0 {
		t.Fatalf("state %s, events %+v", s.State(), events)
	}
	wantStreak(t, s, 1, 1)
}

func TestFastTierRateBetweenTheBarsIsHysteresis(t *testing.T) {
	s := suspended7d(t)
	record(t, s, handovers("mid", 65, true, midday(1).Add(-time.Hour))) // 115 of 125 = 0.92
	events := s.Recompute(midday(1))
	if got := s.Metrics().OnTimeHandoverRate; got < suspendBelow || got >= reinstateAt {
		t.Fatalf("setup: rate = %v, want in [0.90, 0.95)", got)
	}
	if len(tierEvents(events)) != 0 {
		t.Fatalf("0.92 reinstates nothing: %+v", events)
	}
	wantStreak(t, s, 0, 0)
}

func TestFastTierReinstatesOnTheThirdDistinctDay(t *testing.T) {
	s := suspended7d(t)
	lift(t, s, midday(1))
	for day := 1; day <= 2; day++ {
		if tiers := tierEvents(s.Recompute(midday(day))); len(tiers) != 0 {
			t.Fatalf("day %d: not yet: %+v", day, tiers)
		}
		wantStreak(t, s, day, day)
	}
	tiers := tierEvents(s.Recompute(midday(3)))
	if len(tiers) != 2 || tiers[0].Tier != TierSameDay || tiers[1].Tier != TierNextDay {
		t.Fatalf("day 3 reinstates both tiers: %+v", tiers)
	}
	rate := s.Metrics().OnTimeHandoverRate
	for _, e := range tiers {
		if !e.Eligible || e.Threshold != 0.95 || e.Value != rate || !e.ChangedAt.Equal(midday(3)) || e.Metric != MetricOnTimeHandoverRate {
			t.Errorf("reinstatement event = %+v (rate %v)", e, rate)
		}
	}
	if s.FastTier() != (FastTierState{}) {
		t.Errorf("reinstatement clears the streak: %+v", s.FastTier())
	}
}

func TestFastTierSameDayTwiceCountsOnce(t *testing.T) {
	s := suspended7d(t)
	lift(t, s, midday(1))
	s.Recompute(midday(1))
	s.Recompute(midday(1).Add(3 * time.Hour))
	s.Recompute(midday(1).Add(6 * time.Hour))
	wantStreak(t, s, 1, 1)
	s.Recompute(midday(2))
	s.Recompute(midday(2).Add(time.Hour))
	wantStreak(t, s, 2, 2)
}

func TestFastTierADayBelowTheReinstateBarResetsTheStreak(t *testing.T) {
	s := suspended7d(t)
	lift(t, s, midday(1))
	s.Recompute(midday(1))
	s.Recompute(midday(2))
	wantStreak(t, s, 2, 2)
	record(t, s, handovers("bad", 150, false, midday(3).Add(-time.Hour))) // 450 of 610 = 0.74
	s.Recompute(midday(3))
	wantStreak(t, s, 0, 0)
}

func TestFastTierTheWindowOfThirtyDaysNeverEvaluatesThePolicy(t *testing.T) {
	s := open(t, Window30d, handovers("late", 60, false, base)...)
	events := s.Recompute(midday(0))
	if len(tierEvents(events)) != 0 || !s.FastTier().Eligible() {
		t.Fatalf("only the 7d scorecard gates the fast tiers: %+v", events)
	}
}
