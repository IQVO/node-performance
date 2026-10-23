package scorecard

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestNewMetricsAcceptsTheBounds(t *testing.T) {
	for _, m := range []Metrics{
		{},
		{OnTimeHandoverRate: 1, LateAckRate: 1, RejectRate: 1, ExpiryRate: 1, PromiseKeptRate: 1, FeedFreshnessP95Minutes: 0, Observations: shared.MaxInt32},
		EmptyMetrics(),
		{OnTimeHandoverRate: 0.5, FeedFreshnessP95Minutes: 1 << 30, Observations: 7},
	} {
		got, err := NewMetrics(m)
		if err != nil || got != m {
			t.Errorf("NewMetrics(%+v) = %+v, %v", m, got, err)
		}
	}
}

func TestNewMetricsRefusesWhatBreaksAnInvariant(t *testing.T) {
	rateSetters := map[string]func(*Metrics, float64){
		"onTimeHandoverRate": func(m *Metrics, v float64) { m.OnTimeHandoverRate = v },
		"lateAckRate":        func(m *Metrics, v float64) { m.LateAckRate = v },
		"rejectRate":         func(m *Metrics, v float64) { m.RejectRate = v },
		"expiryRate":         func(m *Metrics, v float64) { m.ExpiryRate = v },
		"promiseKeptRate":    func(m *Metrics, v float64) { m.PromiseKeptRate = v },
	}
	for name, set := range rateSetters {
		for _, bad := range []float64{-0.0001, 1.0001, math.NaN(), math.Inf(1), math.Inf(-1)} {
			var m Metrics
			set(&m, bad)
			if _, err := NewMetrics(m); !errors.Is(err, ErrRateOutOfRange) {
				t.Errorf("%s = %v must be ErrRateOutOfRange, got %v", name, bad, err)
			}
		}
	}
	for name, m := range map[string]Metrics{
		"negative freshness":         {FeedFreshnessP95Minutes: -1},
		"negative observations":      {Observations: -1},
		"observations over MaxInt32": {Observations: shared.MaxInt32 + 1},
	} {
		if _, err := NewMetrics(m); !errors.Is(err, ErrRateOutOfRange) {
			t.Errorf("%s must be ErrRateOutOfRange, got %v", name, err)
		}
	}
}

func TestEmptyMetricsAreTheBestValues(t *testing.T) {
	m := EmptyMetrics()
	want := Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 1}
	if m != want {
		t.Fatalf("EmptyMetrics() = %+v, want %+v", m, want)
	}
	if got := Compute(nil); got != want {
		t.Fatalf("Compute(nil) = %+v, want %+v", got, want)
	}
}

func combine(lists ...[]Observation) []Observation {
	var out []Observation
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func withKind(list []Observation, mark func(*Observation)) []Observation {
	for i := range list {
		mark(&list[i])
	}
	return list
}

func TestComputeRates(t *testing.T) {
	onTimeOf := func(prefix string, n, late int) []Observation {
		l := many(prefix, KindHandover, n, at(0))
		for i := 0; i < late; i++ {
			l[i].OnTime = false
		}
		return l
	}
	acks := combine(
		withKind(many("acc", KindAckAccepted, 6, at(0)), func(o *Observation) { o.OnTime = false }),
		withKind(many("rej", KindAckRejected, 2, at(0)), func(o *Observation) { o.OnTime = false }),
		withKind(many("exp", KindAckExpired, 2, at(0)), func(o *Observation) { o.OnTime = false; o.Late = true }),
	)
	acks[0].Late = true // one late acceptance
	acks[6].Late = true // one late rejection

	for _, tc := range []struct {
		name string
		in   []Observation
		want Metrics
	}{
		{"handovers: 3 of 4 on time", onTimeOf("h", 4, 1), Metrics{OnTimeHandoverRate: 0.75, PromiseKeptRate: 1, Observations: 4}},
		{"handovers: none on time", onTimeOf("h", 2, 2), Metrics{OnTimeHandoverRate: 0, PromiseKeptRate: 1, Observations: 2}},
		{"acks: late, reject and expiry over 10 resolved orders", acks, Metrics{OnTimeHandoverRate: 1, LateAckRate: 0.4, RejectRate: 0.2, ExpiryRate: 0.2, PromiseKeptRate: 1, Observations: 10}},
		{"promises: 3 kept 1 broken", combine(many("k", KindPromiseKept, 3, at(0)), many("b", KindPromiseBroken, 1, at(0))),
			Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 0.75, Observations: 4}},
		{"promises: only broken", many("b", KindPromiseBroken, 2, at(0)), Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 0, Observations: 2}},
		{"promises: only kept", many("k", KindPromiseKept, 2, at(0)), Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 1, Observations: 2}},
		{"a single feed has no gap", many("f", KindFeedApplied, 1, at(0)), Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 1, Observations: 1}},
	} {
		got := Compute(tc.in)
		if got != tc.want {
			t.Errorf("%s: Compute = %+v, want %+v", tc.name, got, tc.want)
		}
		if _, err := NewMetrics(got); err != nil {
			t.Errorf("%s: Compute must satisfy NewMetrics: %v", tc.name, err)
		}
	}
}

func feedsAt(offsets ...time.Duration) []Observation {
	out := make([]Observation, len(offsets))
	for i, o := range offsets {
		out[i] = obs(fmt.Sprintf("feed-%d", i), KindFeedApplied, base.Add(o))
	}
	return out
}

func TestFeedFreshnessGapsAreWholeMinutesBetweenConsecutiveFeeds(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []Observation
		want int
	}{
		{"no feed", nil, 0},
		{"one feed", feedsAt(0), 0},
		{"one gap of 10 minutes", feedsAt(0, 10*time.Minute), 10},
		{"a gap of 90 seconds truncates to 1 minute", feedsAt(0, 90*time.Second), 1},
		{"a gap of 59 seconds is 0 minutes", feedsAt(0, 59*time.Second), 0},
		{"feeds out of order are sorted first", feedsAt(30*time.Minute, 0, 10*time.Minute), 20},
		{"ties on an instant make a zero gap", feedsAt(0, 0, 5*time.Minute), 5},
	} {
		if got := Compute(tc.in).FeedFreshnessP95Minutes; got != tc.want {
			t.Errorf("%s: p95 = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestFeedFreshnessIsTheNearestRankP95(t *testing.T) {
	// n gaps of 1..n minutes; the nearest rank is ceil(0.95*n).
	for _, tc := range []struct{ gaps, want int }{
		{1, 1}, {2, 2}, {10, 10}, {19, 19}, {20, 19}, {21, 20}, {40, 38}, {100, 95}, {101, 96},
	} {
		offsets := []time.Duration{0}
		cur := time.Duration(0)
		for g := 1; g <= tc.gaps; g++ {
			cur += time.Duration(g) * time.Minute
			offsets = append(offsets, cur)
		}
		if got := Compute(feedsAt(offsets...)).FeedFreshnessP95Minutes; got != tc.want {
			t.Errorf("%d gaps: p95 = %d, want %d", tc.gaps, got, tc.want)
		}
	}
}

func TestComputeCountsEveryObservation(t *testing.T) {
	list := combine(
		many("a", KindAckAccepted, 2, at(0)), many("h", KindHandover, 3, at(0)),
		many("k", KindPromiseKept, 4, at(0)), many("f", KindFeedApplied, 5, at(0)),
	)
	if got := Compute(list).Observations; got != 14 {
		t.Fatalf("observations = %d, want 14", got)
	}
}
