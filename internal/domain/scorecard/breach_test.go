package scorecard

import (
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// gradedAt returns a recomputed 7d scorecard of 50 handovers, onTime of them on time.
func gradedAt(t *testing.T, onTime int) (*NodeScorecard, time.Time) {
	t.Helper()
	list := many("h", KindHandover, MinObservations, at(0))
	for i := onTime; i < len(list); i++ {
		list[i].OnTime = false
	}
	s := open(t, Window7d, list...)
	s.Recompute(at(100))
	return s, at(101)
}

func TestRecomputeRaisesAThresholdBreachWhenAMetricCrossesDownward(t *testing.T) {
	s, now := gradedAt(t, 50) // on-time 1.0
	// five late handovers: 50 of 55 on time = 0.909, still healthy
	for i := 0; i < 5; i++ {
		if err := s.RecordObservation(obs(fmtID("late", i), KindHandover, at(10+i))); err != nil {
			t.Fatal(err)
		}
	}
	events := s.Recompute(now)
	if len(events) != 1 || events[0].EventName() != "NodeScorecardUpdated" {
		t.Fatalf("0.909 is still healthy, no breach: %+v", events)
	}
	// one more late handover: 50 of 56 = 0.892 < 0.90
	if err := s.RecordObservation(obs("late-5", KindHandover, at(20))); err != nil {
		t.Fatal(err)
	}
	events = s.Recompute(now.Add(time.Minute))
	// the 7d rate fell under 0.90 too: the update, the breach, then the two tier events
	if len(events) != 4 || events[2].EventName() != "NodeTierEligibilityChanged" || events[3].EventName() != "NodeTierEligibilityChanged" {
		t.Fatalf("events = %+v, want an update, a breach and two tier events", events)
	}
	assertBreach(t, events[1], s, NodeThresholdBreached{
		Header: shared.Header{Version: s.Version(), At: now.Add(time.Minute)}, NodeID: "node-0f3a91", Window: Window7d,
		Metric: MetricOnTimeHandoverRate, Value: 50.0 / 56.0, Threshold: 0.90,
	})
	assertUpdateAccessors(t, events[0])
}

func assertBreach(t *testing.T, got shared.Event, s *NodeScorecard, want NodeThresholdBreached) {
	t.Helper()
	breach, ok := got.(NodeThresholdBreached)
	if !ok {
		t.Fatalf("event is %T, want NodeThresholdBreached", got)
	}
	if breach != want {
		t.Errorf("breach = %+v\nwant     %+v", breach, want)
	}
	if breach.EventName() != "NodeThresholdBreached" || breach.Entity() != "scorecard" || !breach.AnalyticsOnly() {
		t.Errorf("breach classification: %s %s analyticsOnly=%v", breach.EventName(), breach.Entity(), breach.AnalyticsOnly())
	}
	if breach.SubjectID() != "node-0f3a91:7d" || breach.AggregateVersion() != s.Version() || !breach.OccurredAt().Equal(want.At) {
		t.Errorf("breach identity: %s v%d %v", breach.SubjectID(), breach.AggregateVersion(), breach.OccurredAt())
	}
}

func assertUpdateAccessors(t *testing.T, got shared.Event) {
	t.Helper()
	if got.AnalyticsOnly() || got.Entity() != "scorecard" || got.EventName() != "NodeScorecardUpdated" {
		t.Errorf("update classification: %s %s analyticsOnly=%v", got.EventName(), got.Entity(), got.AnalyticsOnly())
	}
}

func fmtID(prefix string, i int) string { return prefix + "-" + string(rune('0'+i)) }

func TestRecomputeNeverBreachesWithoutAGradedBaseline(t *testing.T) {
	// A first graded recompute that is already unhealthy has no baseline.
	list := many("h", KindHandover, MinObservations, at(0))
	for i := range list {
		list[i].OnTime = false
	}
	s := open(t, Window7d, list...)
	events := s.Recompute(at(100))
	if len(events) != 3 || events[0].EventName() != "NodeScorecardUpdated" || events[1].EventName() != "NodeTierEligibilityChanged" {
		t.Fatalf("a first graded recompute publishes the update (and the suspension), never a breach: %+v", events)
	}
	if s.Grade() != GradeD {
		t.Errorf("grade = %q, want D", s.Grade())
	}
}
