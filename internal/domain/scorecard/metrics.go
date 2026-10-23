package scorecard

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// MinObservations is the least number of observations in a window for a grade
// (G33).
const MinObservations = 50

// p95Rank is the percentile of the freshness metric, in percent.
const p95Rank = 95

// Metrics is the value object of a scorecard (ADR 0002, A5-A9).
type Metrics struct {
	// OnTimeHandoverRate: on-time handovers over handovers. Higher is better.
	OnTimeHandoverRate float64
	// LateAckRate: late acknowledgements plus expiries over acknowledged or
	// expired orders. Lower is better.
	LateAckRate float64
	// RejectRate: rejected over acknowledged or expired orders. Lower is better.
	RejectRate float64
	// ExpiryRate: expired over acknowledged or expired orders. Lower is better.
	ExpiryRate float64
	// PromiseKeptRate: kept over kept plus node-attributable broken. Higher is better.
	PromiseKeptRate float64
	// FeedFreshnessP95Minutes: nearest-rank p95 of the whole minutes between
	// consecutive feeds. Lower is better.
	FeedFreshnessP95Minutes int
	// Observations counted in the window.
	Observations int
}

// NewMetrics validates m: every rate in [0,1] and not NaN, a non-negative
// freshness, an observation count within [0, MaxInt32].
func NewMetrics(m Metrics) (Metrics, error) {
	rates := []struct {
		name string
		v    float64
	}{
		{"onTimeHandoverRate", m.OnTimeHandoverRate},
		{"lateAckRate", m.LateAckRate},
		{"rejectRate", m.RejectRate},
		{"expiryRate", m.ExpiryRate},
		{"promiseKeptRate", m.PromiseKeptRate},
	}
	for _, r := range rates {
		if math.IsNaN(r.v) || r.v < 0 || r.v > 1 {
			return Metrics{}, fmt.Errorf("%w: %s = %v is outside [0,1]", ErrRateOutOfRange, r.name, r.v)
		}
	}
	if m.FeedFreshnessP95Minutes < 0 {
		return Metrics{}, fmt.Errorf("%w: feedFreshnessP95Minutes = %d is negative", ErrRateOutOfRange, m.FeedFreshnessP95Minutes)
	}
	if m.Observations < 0 || m.Observations > shared.MaxInt32 {
		return Metrics{}, fmt.Errorf("%w: observations = %d is outside [0,%d]", ErrRateOutOfRange, m.Observations, shared.MaxInt32)
	}
	return m, nil
}

// EmptyMetrics is the best value of every metric with no observation (A5).
func EmptyMetrics() Metrics {
	return Metrics{OnTimeHandoverRate: 1, PromiseKeptRate: 1}
}

type tally struct {
	handovers, onTime int
	acks, late        int
	rejected, expired int
	kept, broken      int
	feeds             []time.Time
}

func tallyOf(observations []Observation) tally {
	var t tally
	for _, o := range observations {
		switch o.Kind {
		case KindHandover:
			t.handovers++
			if o.OnTime {
				t.onTime++
			}
		case KindAckAccepted, KindAckRejected, KindAckExpired:
			t.countAck(o)
		case KindPromiseKept:
			t.kept++
		case KindPromiseBroken:
			t.broken++
		case KindFeedApplied:
			t.feeds = append(t.feeds, o.At)
		}
	}
	return t
}

func (t *tally) countAck(o Observation) {
	t.acks++
	if o.Late {
		t.late++
	}
	switch o.Kind {
	case KindAckRejected:
		t.rejected++
	case KindAckExpired:
		t.expired++
	}
}

// rate is num/den, or best when the denominator is empty (A5).
func rate(num, den int, best float64) float64 {
	if den == 0 {
		return best
	}
	return float64(num) / float64(den)
}

// Compute returns the metrics of the observations (already restricted to a
// window by the caller). Every rate is a ratio of two counts with the
// numerator never above the denominator, so the result always satisfies
// NewMetrics.
func Compute(observations []Observation) Metrics {
	t := tallyOf(observations)
	return Metrics{
		OnTimeHandoverRate:      rate(t.onTime, t.handovers, 1),
		LateAckRate:             rate(t.late, t.acks, 0),
		RejectRate:              rate(t.rejected, t.acks, 0),
		ExpiryRate:              rate(t.expired, t.acks, 0),
		PromiseKeptRate:         rate(t.kept, t.kept+t.broken, 1),
		FeedFreshnessP95Minutes: feedFreshnessP95(t.feeds),
		Observations:            len(observations),
	}
}

// feedFreshnessP95 is the nearest-rank 95th percentile of the whole minutes
// between consecutive feeds (A9); zero without two feeds.
func feedFreshnessP95(feeds []time.Time) int {
	if len(feeds) < 2 {
		return 0
	}
	sorted := append([]time.Time(nil), feeds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	var gaps []int
	for i := 1; i < len(sorted); i++ {
		gaps = append(gaps, int(sorted[i].Sub(sorted[i-1])/time.Minute))
	}
	sort.Ints(gaps)
	rank := (p95Rank*len(gaps) + 99) / 100
	return gaps[rank-1]
}
