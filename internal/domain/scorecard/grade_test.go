package scorecard

import (
	"errors"
	"testing"
)

// graded returns metrics with 50 observations (the minimum) and the three
// graded metrics given.
func graded(onTime, kept, lateAck float64) Metrics {
	return Metrics{OnTimeHandoverRate: onTime, PromiseKeptRate: kept, LateAckRate: lateAck, Observations: MinObservations}
}

func TestGradeForThePolicyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Metrics
		want Grade
	}{
		{"perfect", graded(1, 1, 0), GradeA},
		{"A at exactly its bars", graded(0.98, 0.97, 0.01), GradeA},
		{"on-time just under A", graded(0.9799, 0.97, 0.01), GradeB},
		{"kept just under A", graded(0.98, 0.9699, 0.01), GradeB},
		{"late ack just over A", graded(0.98, 0.97, 0.0101), GradeB},
		{"B at exactly its bars", graded(0.95, 0.94, 0.03), GradeB},
		{"on-time just under B", graded(0.9499, 0.94, 0.03), GradeC},
		{"kept just under B", graded(0.95, 0.9399, 0.03), GradeC},
		{"late ack just over B", graded(0.95, 0.94, 0.0301), GradeC},
		{"C at exactly its bars", graded(0.90, 0.90, 0.05), GradeC},
		{"on-time just under C", graded(0.8999, 0.90, 0.05), GradeD},
		{"kept just under C", graded(0.90, 0.8999, 0.05), GradeD},
		{"late ack just over C", graded(0.90, 0.90, 0.0501), GradeD},
		{"worst", graded(0, 0, 1), GradeD},
		{"the worst bar decides: great on-time, poor kept", graded(1, 0.91, 0), GradeC},
		{"the worst bar decides: great kept, poor on-time", graded(0.91, 1, 0), GradeC},
		{"the worst bar decides: great rates, poor late ack", graded(1, 1, 0.04), GradeC},
	} {
		got, err := GradeFor(tc.m)
		if err != nil || got != tc.want {
			t.Errorf("%s: GradeFor = %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestGradeForNeedsTheObservationMinimum(t *testing.T) {
	m := graded(1, 1, 0)
	m.Observations = MinObservations - 1
	if g, err := GradeFor(m); !errors.Is(err, ErrInsufficientObservations) || g != "" {
		t.Fatalf("49 observations: GradeFor = %q, %v", g, err)
	}
	m.Observations = MinObservations
	if g, err := GradeFor(m); err != nil || g != GradeA {
		t.Fatalf("50 observations: GradeFor = %q, %v", g, err)
	}
	if MinObservations != 50 {
		t.Fatalf("MinObservations = %d, G33 says 50", MinObservations)
	}
}

func TestGradeLabel(t *testing.T) {
	for _, tc := range []struct {
		s    State
		g    Grade
		want string
	}{
		{StateGraded, GradeA, "A"}, {StateGraded, GradeD, "D"}, {StateProvisional, "", "PROVISIONAL"},
		{StateProvisional, GradeB, "PROVISIONAL"},
	} {
		if got := GradeLabel(tc.s, tc.g); got != tc.want {
			t.Errorf("GradeLabel(%s,%q) = %q, want %q", tc.s, tc.g, got, tc.want)
		}
	}
}

func TestStateAndGradeValidity(t *testing.T) {
	for s, want := range map[State]bool{StateProvisional: true, StateGraded: true, "": false, "DONE": false} {
		if s.Valid() != want {
			t.Errorf("State(%q).Valid() = %v", s, !want)
		}
	}
	for g, want := range map[Grade]bool{GradeA: true, GradeB: true, GradeC: true, GradeD: true, "": false, "E": false, "a": false} {
		if g.Valid() != want {
			t.Errorf("Grade(%q).Valid() = %v", g, !want)
		}
	}
}

func TestThresholdsHealthySide(t *testing.T) {
	want := map[MetricName]struct {
		bound  float64
		higher bool
	}{
		MetricOnTimeHandoverRate: {0.90, true}, MetricPromiseKeptRate: {0.90, true},
		MetricLateAckRate: {0.05, false}, MetricRejectRate: {0.10, false},
		MetricExpiryRate: {0.05, false}, MetricFeedFreshnessP95: {240, false},
	}
	got := Thresholds()
	if len(got) != len(want) {
		t.Fatalf("Thresholds() has %d rows, want %d", len(got), len(want))
	}
	for _, th := range got {
		w, ok := want[th.Metric]
		if !ok || th.Bound != w.bound || th.HigherIsBetter != w.higher {
			t.Errorf("threshold %+v, want %+v", th, w)
		}
		if th.HigherIsBetter {
			if !th.Healthy(th.Bound) || !th.Healthy(th.Bound+0.001) || th.Healthy(th.Bound-0.001) {
				t.Errorf("%s: a higher-is-better bound is healthy at the bound and above only", th.Metric)
			}
		} else if !th.Healthy(th.Bound) || !th.Healthy(th.Bound-0.001) || th.Healthy(th.Bound+0.001) {
			t.Errorf("%s: a lower-is-better bound is healthy at the bound and below only", th.Metric)
		}
	}
}

func TestThresholdsReturnsACopy(t *testing.T) {
	a := Thresholds()
	a[0].Bound = 12345
	if Thresholds()[0].Bound == 12345 {
		t.Fatal("Thresholds() must not expose the table")
	}
}

// healthyMetrics sits on the healthy side of every threshold.
func healthyMetrics() Metrics {
	return Metrics{OnTimeHandoverRate: 0.99, PromiseKeptRate: 0.99, LateAckRate: 0.01, RejectRate: 0.01, ExpiryRate: 0.01, FeedFreshnessP95Minutes: 30, Observations: 100}
}

func TestBreachesOneMetricAtATime(t *testing.T) {
	for _, tc := range []struct {
		name      string
		worsen    func(*Metrics)
		metric    MetricName
		value     float64
		threshold float64
	}{
		{"on-time handover", func(m *Metrics) { m.OnTimeHandoverRate = 0.8999 }, MetricOnTimeHandoverRate, 0.8999, 0.90},
		{"promise kept", func(m *Metrics) { m.PromiseKeptRate = 0.5 }, MetricPromiseKeptRate, 0.5, 0.90},
		{"late ack", func(m *Metrics) { m.LateAckRate = 0.0501 }, MetricLateAckRate, 0.0501, 0.05},
		{"reject", func(m *Metrics) { m.RejectRate = 0.2 }, MetricRejectRate, 0.2, 0.10},
		{"expiry", func(m *Metrics) { m.ExpiryRate = 0.06 }, MetricExpiryRate, 0.06, 0.05},
		{"feed freshness", func(m *Metrics) { m.FeedFreshnessP95Minutes = 241 }, MetricFeedFreshnessP95, 241, 240},
	} {
		next := healthyMetrics()
		tc.worsen(&next)
		got := Breaches(healthyMetrics(), next)
		if len(got) != 1 || got[0] != (Breach{Metric: tc.metric, Value: tc.value, Threshold: tc.threshold}) {
			t.Errorf("%s: Breaches = %+v", tc.name, got)
		}
	}
}

func TestBreachesNeedACrossing(t *testing.T) {
	bad := Metrics{OnTimeHandoverRate: 0.5, PromiseKeptRate: 0.5, LateAckRate: 0.5, RejectRate: 0.5, ExpiryRate: 0.5, FeedFreshnessP95Minutes: 999, Observations: 100}
	for name, c := range map[string]struct{ prev, next Metrics }{
		"unchanged healthy":             {healthyMetrics(), healthyMetrics()},
		"already unhealthy stays so":    {bad, bad},
		"recovery is not a breach":      {bad, healthyMetrics()},
		"landing exactly on the bounds": {healthyMetrics(), Metrics{OnTimeHandoverRate: 0.90, PromiseKeptRate: 0.90, LateAckRate: 0.05, RejectRate: 0.10, ExpiryRate: 0.05, FeedFreshnessP95Minutes: 240, Observations: 100}},
	} {
		if got := Breaches(c.prev, c.next); len(got) != 0 {
			t.Errorf("%s: Breaches = %+v, want none", name, got)
		}
	}
}

func TestBreachesListEveryCrossingInTableOrder(t *testing.T) {
	got := Breaches(healthyMetrics(), Metrics{OnTimeHandoverRate: 0.1, PromiseKeptRate: 0.1, LateAckRate: 0.9, RejectRate: 0.9, ExpiryRate: 0.9, FeedFreshnessP95Minutes: 1000})
	order := []MetricName{MetricOnTimeHandoverRate, MetricPromiseKeptRate, MetricLateAckRate, MetricRejectRate, MetricExpiryRate, MetricFeedFreshnessP95}
	if len(got) != len(order) {
		t.Fatalf("Breaches = %+v, want %d", got, len(order))
	}
	for i, b := range got {
		if b.Metric != order[i] {
			t.Errorf("breach %d = %s, want %s", i, b.Metric, order[i])
		}
	}
}
