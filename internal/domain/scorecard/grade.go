package scorecard

// State is the lifecycle state of a scorecard.
type State string

const (
	// StateProvisional is a scorecard below MinObservations.
	StateProvisional State = "PROVISIONAL"
	// StateGraded is a scorecard with a grade.
	StateGraded State = "GRADED"
)

// Valid reports whether s is a known state.
func (s State) Valid() bool { return s == StateProvisional || s == StateGraded }

// Grade is A, B, C or D; empty while the scorecard is PROVISIONAL.
type Grade string

// The grades, best first.
const (
	GradeA Grade = "A"
	GradeB Grade = "B"
	GradeC Grade = "C"
	GradeD Grade = "D"
)

// Valid reports whether g is one of A, B, C, D.
func (g Grade) Valid() bool { return g == GradeA || g == GradeB || g == GradeC || g == GradeD }

// GradeLabel is the wire value of the (state, grade) pair: the grade when
// graded, PROVISIONAL otherwise (G20).
func GradeLabel(s State, g Grade) string {
	if s == StateGraded {
		return string(g)
	}
	return string(StateProvisional)
}

// gradeBar is one row of the Grading policy: the least on-time handover and
// promise-kept rates and the most late-ack rate of a grade.
type gradeBar struct {
	grade                   Grade
	onTime, kept, lateAckAt float64
}

// gradingBars is the model's Grading policy, best grade first; D is whatever
// meets none.
var gradingBars = [...]gradeBar{
	{GradeA, 0.98, 0.97, 0.01},
	{GradeB, 0.95, 0.94, 0.03},
	{GradeC, 0.90, 0.90, 0.05},
}

// GradeFor applies the Grading policy to m. It refuses to grade a window with
// fewer than MinObservations (ErrInsufficientObservations).
func GradeFor(m Metrics) (Grade, error) {
	if m.Observations < MinObservations {
		return "", ErrInsufficientObservations
	}
	for _, b := range gradingBars {
		if m.OnTimeHandoverRate >= b.onTime && m.PromiseKeptRate >= b.kept && m.LateAckRate <= b.lateAckAt {
			return b.grade, nil
		}
	}
	return GradeD, nil
}

// MetricName names a field of Metrics on the wire.
type MetricName string

// The metrics an alert can name.
const (
	MetricOnTimeHandoverRate MetricName = "onTimeHandoverRate"
	MetricLateAckRate        MetricName = "lateAckRate"
	MetricRejectRate         MetricName = "rejectRate"
	MetricExpiryRate         MetricName = "expiryRate"
	MetricPromiseKeptRate    MetricName = "promiseKeptRate"
	MetricFeedFreshnessP95   MetricName = "feedFreshnessP95Minutes"
)

// Threshold is the alert bound of one metric (ADR 0002, A11).
type Threshold struct {
	Metric MetricName
	Bound  float64
	// HigherIsBetter: the healthy side is value >= Bound; otherwise value <= Bound.
	HigherIsBetter bool
}

// thresholds is the ThresholdAlert table: the C bounds of the graded metrics,
// defaults for the others.
var thresholds = [...]Threshold{
	{MetricOnTimeHandoverRate, 0.90, true},
	{MetricPromiseKeptRate, 0.90, true},
	{MetricLateAckRate, 0.05, false},
	{MetricRejectRate, 0.10, false},
	{MetricExpiryRate, 0.05, false},
	{MetricFeedFreshnessP95, 240, false},
}

// Thresholds returns a copy of the alert table.
func Thresholds() []Threshold { return append([]Threshold(nil), thresholds[:]...) }

// Healthy reports whether v is on the healthy side of the bound (the bound
// itself is healthy).
func (t Threshold) Healthy(v float64) bool {
	if t.HigherIsBetter {
		return v >= t.Bound
	}
	return v <= t.Bound
}

// value reads the metric a threshold names.
func (t Threshold) value(m Metrics) float64 {
	switch t.Metric {
	case MetricOnTimeHandoverRate:
		return m.OnTimeHandoverRate
	case MetricLateAckRate:
		return m.LateAckRate
	case MetricRejectRate:
		return m.RejectRate
	case MetricExpiryRate:
		return m.ExpiryRate
	case MetricPromiseKeptRate:
		return m.PromiseKeptRate
	default:
		return float64(m.FeedFreshnessP95Minutes)
	}
}

// Breach is one metric that crossed its threshold downward.
type Breach struct {
	Metric    MetricName
	Value     float64
	Threshold float64
}

// Breaches returns the metrics that moved from the healthy to the unhealthy
// side of their threshold between prev and next, in table order.
func Breaches(prev, next Metrics) []Breach {
	var out []Breach
	for _, t := range thresholds {
		if t.Healthy(t.value(prev)) && !t.Healthy(t.value(next)) {
			out = append(out, Breach{Metric: t.Metric, Value: t.value(next), Threshold: t.Bound})
		}
	}
	return out
}
