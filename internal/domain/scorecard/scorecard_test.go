package scorecard

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestOpenCreatesAProvisionalScorecardFromTheFirstObservation(t *testing.T) {
	first := obs("evt-1", KindFeedApplied, at(5))
	s, err := Open(mustNode(t), Window30d, first)
	if err != nil {
		t.Fatal(err)
	}
	if s.State() != StateProvisional || s.Grade() != "" || s.GradeLabel() != "PROVISIONAL" {
		t.Errorf("state %s grade %q label %q", s.State(), s.Grade(), s.GradeLabel())
	}
	if s.Version() != 1 || s.ObservationCount() != 1 || !s.ComputedAt().IsZero() {
		t.Errorf("version %d observations %d computedAt %v", s.Version(), s.ObservationCount(), s.ComputedAt())
	}
	if s.Metrics() != EmptyMetrics() {
		t.Errorf("metrics before the first recompute = %+v", s.Metrics())
	}
	if s.ID() != "node-0f3a91:30d" || s.NodeID() != "node-0f3a91" || s.Window() != Window30d {
		t.Errorf("identity %q %q %q", s.ID(), s.NodeID(), s.Window())
	}
	if !s.DueForRecompute(at(5)) {
		t.Error("a new scorecard is due for a recompute")
	}
}

func TestOpenRefusesInvalidInput(t *testing.T) {
	good := obs("evt-1", KindFeedApplied, at(5))
	for name, run := range map[string]func() (*NodeScorecard, error){
		"node with a colon":    func() (*NodeScorecard, error) { return Open("a:b", Window7d, good) },
		"empty node":           func() (*NodeScorecard, error) { return Open("", Window7d, good) },
		"unknown window":       func() (*NodeScorecard, error) { return Open("n", "1d", good) },
		"observation no id":    func() (*NodeScorecard, error) { return Open("n", Window7d, obs("", KindFeedApplied, at(1))) },
		"observation no time":  func() (*NodeScorecard, error) { return Open("n", Window7d, obs("e", KindFeedApplied, time.Time{})) },
		"observation bad kind": func() (*NodeScorecard, error) { return Open("n", Window7d, obs("e", "NOPE", at(1))) },
	} {
		s, err := run()
		if s != nil || !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: got %v, %v, want ErrValidation", name, s, err)
		}
		if errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: a bad command input is a validation error, not a corrupt snapshot", name)
		}
	}
}

func TestRecordObservationCountsOnceAndDirties(t *testing.T) {
	s := open(t, Window7d, obs("evt-1", KindFeedApplied, at(5)))
	s.Recompute(at(10)) // clears dirty, version 2
	if s.DueForRecompute(at(10)) {
		t.Fatal("a freshly recomputed scorecard is not due")
	}
	before := s.Version()
	if err := s.RecordObservation(obs("evt-2", KindFeedApplied, at(15))); err != nil {
		t.Fatal(err)
	}
	if s.ObservationCount() != 2 || s.Version() != before+1 || !s.DueForRecompute(at(15)) {
		t.Errorf("after record: observations %d version %d due %v", s.ObservationCount(), s.Version(), s.DueForRecompute(at(15)))
	}
	if s.State() != StateProvisional {
		t.Error("RecordObservation changes no state")
	}
}

func TestRecordObservationRefusals(t *testing.T) {
	s := open(t, Window7d, obs("evt-1", KindFeedApplied, at(5)))
	v := s.Version()
	if err := s.RecordObservation(obs("evt-1", KindHandover, at(99))); !errors.Is(err, ErrDuplicateObservation) {
		t.Fatalf("a source id counts once: %v", err)
	}
	for name, o := range map[string]Observation{
		"no id":    obs("", KindFeedApplied, at(1)),
		"no time":  obs("e", KindFeedApplied, time.Time{}),
		"bad kind": obs("e", "NOPE", at(1)),
	} {
		if err := s.RecordObservation(o); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if s.Version() != v || s.ObservationCount() != 1 {
		t.Errorf("a refused observation changes nothing: version %d observations %d", s.Version(), s.ObservationCount())
	}
}

func TestRecordObservationNormalisesTheInstantToUTC(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	s := open(t, Window7d, obs("evt-1", KindFeedApplied, at(5)))
	if err := s.RecordObservation(obs("evt-2", KindFeedApplied, time.Date(2026, 10, 22, 9, 0, 0, 0, brt))); err != nil {
		t.Fatal(err)
	}
	for _, o := range s.Snapshot().Observations {
		if o.At.Location() != time.UTC {
			t.Errorf("%s instant is not UTC: %v", o.SourceEventID, o.At)
		}
	}
}

func TestRecomputeBelowTheMinimumStaysProvisionalAndPublishesNothing(t *testing.T) {
	list := many("h", KindHandover, MinObservations-1, at(0))
	s := open(t, Window30d, list...)
	events := s.Recompute(at(100))
	if len(events) != 0 {
		t.Fatalf("PROVISIONAL to PROVISIONAL publishes nothing: %+v", events)
	}
	if s.State() != StateProvisional || s.Grade() != "" || s.Metrics().Observations != MinObservations-1 {
		t.Errorf("state %s grade %q metrics %+v", s.State(), s.Grade(), s.Metrics())
	}
	if !s.ComputedAt().Equal(at(100)) || s.DueForRecompute(at(100)) || s.Version() != int64(len(list))+1 {
		t.Errorf("computedAt %v due %v version %d", s.ComputedAt(), s.DueForRecompute(at(100)), s.Version())
	}
}

func TestRecomputeAtTheMinimumGradesAndPublishes(t *testing.T) {
	s := open(t, Window30d, many("h", KindHandover, MinObservations, at(0))...)
	version := s.Version()
	events := s.Recompute(at(100))
	if s.State() != StateGraded || s.Grade() != GradeA || s.GradeLabel() != "A" {
		t.Fatalf("state %s grade %q", s.State(), s.Grade())
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one NodeScorecardUpdated", events)
	}
	ev, ok := events[0].(NodeScorecardUpdated)
	if !ok {
		t.Fatalf("event is %T", events[0])
	}
	want := NodeScorecardUpdated{
		Header: shared.Header{Version: version + 1, At: at(100)}, NodeID: "node-0f3a91", Window: Window30d,
		State: StateGraded, Grade: GradeA, ComputedAt: at(100),
		Metrics: Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 1, Observations: MinObservations},
	}
	if ev != want {
		t.Errorf("event = %+v\nwant    %+v", ev, want)
	}
	if ev.GradeLabel() != "A" || ev.SubjectID() != "node-0f3a91:30d" || ev.AggregateVersion() != version+1 || !ev.OccurredAt().Equal(at(100)) {
		t.Errorf("event accessors: %q %q %d %v", ev.GradeLabel(), ev.SubjectID(), ev.AggregateVersion(), ev.OccurredAt())
	}
}

func TestRecomputeOnlyPublishesWhenSomethingChanged(t *testing.T) {
	s := open(t, Window30d, many("h", KindHandover, MinObservations, at(0))...)
	if got := s.Recompute(at(100)); len(got) != 1 {
		t.Fatalf("first graded recompute: %+v", got)
	}
	if got := s.Recompute(at(101)); len(got) != 0 {
		t.Fatalf("an unchanged recompute publishes nothing: %+v", got)
	}
	if !s.ComputedAt().Equal(at(101)) {
		t.Error("an unchanged recompute still stamps computedAt")
	}
	// a worse handover changes the metrics, not the grade
	late := obs("late-1", KindHandover, at(50))
	late.OnTime = false
	if err := s.RecordObservation(late); err != nil {
		t.Fatal(err)
	}
	got := s.Recompute(at(102))
	if len(got) != 1 || got[0].EventName() != "NodeScorecardUpdated" {
		t.Fatalf("a metric change publishes one update: %+v", got)
	}
}

func TestRecomputeFallingBackToProvisionalPublishesTheFall(t *testing.T) {
	s := open(t, Window7d, many("h", KindHandover, MinObservations, at(0))...)
	s.Recompute(at(100))
	// all observations leave the 7-day window: GRADED -> PROVISIONAL
	events := s.Recompute(at(0).Add(8 * 24 * time.Hour))
	if s.State() != StateProvisional || s.Grade() != "" || s.Metrics() != EmptyMetrics() {
		t.Fatalf("state %s grade %q metrics %+v", s.State(), s.Grade(), s.Metrics())
	}
	if len(events) != 1 {
		t.Fatalf("GRADED to PROVISIONAL publishes the fall: %+v", events)
	}
	ev := events[0].(NodeScorecardUpdated)
	if ev.GradeLabel() != "PROVISIONAL" || ev.Metrics.Observations != 0 {
		t.Errorf("event = %+v", ev)
	}
	if s.ObservationCount() != 0 {
		t.Errorf("observations that left the window are dropped: %d", s.ObservationCount())
	}
}

func TestRecomputeBackToGradedFromProvisional(t *testing.T) {
	s := open(t, Window7d, many("h", KindHandover, MinObservations, at(0))...)
	s.Recompute(at(100))
	s.Recompute(at(0).Add(8 * 24 * time.Hour)) // PROVISIONAL again
	fresh := many("n", KindHandover, MinObservations, at(8*24*60))
	for _, o := range fresh {
		if err := s.RecordObservation(o); err != nil {
			t.Fatal(err)
		}
	}
	events := s.Recompute(at(0).Add(8*24*time.Hour + 2*time.Hour))
	if s.State() != StateGraded || len(events) != 1 {
		t.Fatalf("PROVISIONAL to GRADED: state %s events %+v", s.State(), events)
	}
}

func TestRecomputeWindowEdges(t *testing.T) {
	now := at(100 * 24 * 60)
	inside := now.Add(-Window7d.Duration()).Add(time.Nanosecond)
	onEdge := now.Add(-Window7d.Duration())
	s := open(t, Window7d,
		obs("inside", KindFeedApplied, inside),
		obs("edge", KindFeedApplied, onEdge),
		obs("old", KindFeedApplied, onEdge.Add(-time.Hour)),
		obs("future", KindFeedApplied, now.Add(time.Hour)),
	)
	s.Recompute(now)
	got := map[string]bool{}
	for _, o := range s.Snapshot().Observations {
		got[o.SourceEventID] = true
	}
	if !got["inside"] || !got["future"] || got["edge"] || got["old"] || len(got) != 2 {
		t.Fatalf("kept observations = %v, want inside and future only", got)
	}
	if s.Metrics().Observations != 2 {
		t.Errorf("metrics count the window only: %d", s.Metrics().Observations)
	}
}

func TestEachWindowKeepsItsOwnObservations(t *testing.T) {
	now := at(100 * 24 * 60)
	tenDays := now.Add(-10 * 24 * time.Hour)
	for w, want := range map[Window]int{Window7d: 0, Window30d: 1} {
		s := open(t, w, obs("e", KindFeedApplied, tenDays))
		s.Recompute(now)
		if s.ObservationCount() != want {
			t.Errorf("%s keeps %d observations of a ten-day-old fact, want %d", w, s.ObservationCount(), want)
		}
	}
}

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
	if len(events) != 2 {
		t.Fatalf("events = %+v, want an update and a breach", events)
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
	if len(events) != 1 || events[0].EventName() != "NodeScorecardUpdated" {
		t.Fatalf("a first graded recompute publishes the update only: %+v", events)
	}
	if s.Grade() != GradeD {
		t.Errorf("grade = %q, want D", s.Grade())
	}
}

func TestDueForRecomputeAndExpiry(t *testing.T) {
	s := open(t, Window7d, obs("late", KindFeedApplied, at(200)), obs("early", KindFeedApplied, at(100)))
	s.Recompute(at(300))
	exp, ok := s.ExpiresAt()
	if !ok || !exp.Equal(at(100).Add(Window7d.Duration())) {
		t.Fatalf("ExpiresAt = %v, %v; want the oldest instant plus the window", exp, ok)
	}
	if s.DueForRecompute(exp.Add(-time.Nanosecond)) {
		t.Error("not due a nanosecond before the oldest observation leaves")
	}
	if !s.DueForRecompute(exp) || !s.DueForRecompute(exp.Add(time.Nanosecond)) {
		t.Error("due once the oldest observation leaves the window")
	}
	empty := open(t, Window7d, obs("only", KindFeedApplied, at(100)))
	empty.Recompute(at(100).Add(8 * 24 * time.Hour))
	if _, ok := empty.ExpiresAt(); ok || empty.DueForRecompute(at(100).Add(20*24*time.Hour)) {
		t.Error("a scorecard with no observation never expires and is not due")
	}
}
