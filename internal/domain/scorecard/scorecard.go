package scorecard

import (
	"slices"
	"strings"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// NodeScorecard is the aggregate root: the rolling-window performance of one
// node. Two exist per node, one per Window.
type NodeScorecard struct {
	nodeID       NodeID
	window       Window
	state        State
	grade        Grade
	metrics      Metrics
	computedAt   time.Time
	dirty        bool
	version      int64
	observations map[string]Observation
}

// Snapshot is the persisted form of a scorecard. Restore validates it.
type Snapshot struct {
	NodeID  NodeID
	Window  Window
	State   State
	Grade   Grade
	Metrics Metrics
	// ComputedAt is zero until the first Recompute.
	ComputedAt time.Time
	// Dirty is true while observations were recorded since the last Recompute.
	Dirty        bool
	Version      int64
	Observations []Observation
}

// Open creates the scorecard of one window from the first observation of the
// node: PROVISIONAL, version 1, due for a recompute (G33, ADR 0002, A1).
func Open(nodeID NodeID, window Window, first Observation) (*NodeScorecard, error) {
	return build(Snapshot{
		NodeID: nodeID, Window: window, State: StateProvisional, Metrics: EmptyMetrics(),
		Dirty: true, Version: 1, Observations: []Observation{first},
	}, false)
}

// Restore rebuilds a persisted scorecard, validating every invariant.
func Restore(s Snapshot) (*NodeScorecard, error) {
	return build(s, true)
}

func build(s Snapshot, restoring bool) (*NodeScorecard, error) {
	fail := func(err error) (*NodeScorecard, error) {
		if restoring {
			return nil, shared.Corrupt("scorecard %q: %v", ScorecardID(s.NodeID, s.Window), err)
		}
		return nil, err
	}
	if err := checkIdentity(s); err != nil {
		return fail(err)
	}
	if err := checkState(s); err != nil {
		return fail(err)
	}
	obs, err := indexObservations(s.Observations)
	if err != nil {
		return fail(err)
	}
	return &NodeScorecard{
		nodeID: s.NodeID, window: s.Window, state: s.State, grade: s.Grade, metrics: s.Metrics,
		computedAt: shared.UTC(s.ComputedAt), dirty: s.Dirty, version: s.Version, observations: obs,
	}, nil
}

func checkIdentity(s Snapshot) error {
	if _, err := NewNodeID(string(s.NodeID)); err != nil {
		return err
	}
	if !s.Window.Valid() {
		return shared.Invalid("window must be 7d or 30d")
	}
	if s.Version < 1 {
		return shared.Invalid("version must be at least 1")
	}
	return nil
}

func checkState(s Snapshot) error {
	if !s.State.Valid() {
		return shared.Invalid("unknown state %q", s.State)
	}
	if _, err := NewMetrics(s.Metrics); err != nil {
		return err
	}
	switch s.State {
	case StateGraded:
		if !s.Grade.Valid() {
			return shared.Invalid("a GRADED scorecard needs a grade of A to D, got %q", s.Grade)
		}
		if s.Metrics.Observations < MinObservations {
			return shared.Invalid("a GRADED scorecard needs at least %d observations, got %d", MinObservations, s.Metrics.Observations)
		}
	default:
		if s.Grade != "" {
			return shared.Invalid("a PROVISIONAL scorecard has no grade, got %q", s.Grade)
		}
	}
	return nil
}

func indexObservations(list []Observation) (map[string]Observation, error) {
	out := make(map[string]Observation, len(list))
	for _, o := range list {
		if err := o.validate(); err != nil {
			return nil, err
		}
		if _, dup := out[o.SourceEventID]; dup {
			return nil, shared.Invalid("observation %q appears twice", o.SourceEventID)
		}
		o.At = shared.UTC(o.At)
		out[o.SourceEventID] = o
	}
	return out, nil
}

// RecordObservation counts one observation (command RecordObservation). It
// changes no state and raises no event; the scorecard is due for a recompute.
// A source event id already counted is ErrDuplicateObservation.
func (s *NodeScorecard) RecordObservation(o Observation) error {
	if err := o.validate(); err != nil {
		return err
	}
	if _, dup := s.observations[o.SourceEventID]; dup {
		return ErrDuplicateObservation
	}
	o.At = shared.UTC(o.At)
	s.observations[o.SourceEventID] = o
	s.dirty = true
	s.version++
	return nil
}

// Recompute drops the observations that left the window, recomputes the
// metrics and grades the scorecard (command Recompute). It returns the events
// to publish: NodeScorecardUpdated when the state, grade or metrics changed
// (never PROVISIONAL to PROVISIONAL) and one NodeThresholdBreached per metric
// that crossed its threshold between two GRADED states (ADR 0002, A11, A13).
func (s *NodeScorecard) Recompute(now time.Time) []shared.Event {
	now = now.UTC()
	kept := s.inWindow(now)
	metrics := Compute(kept)
	// GradeFor's only error is ErrInsufficientObservations: below the minimum
	// the scorecard is PROVISIONAL (G33).
	state := StateGraded
	grade, err := GradeFor(metrics)
	if err != nil {
		state, grade = StateProvisional, ""
	}
	prev := *s
	s.observations = indexByID(kept)
	s.state, s.grade, s.metrics = state, grade, metrics
	s.computedAt, s.dirty = now, false
	s.version++
	return s.eventsSince(prev)
}

func (s *NodeScorecard) eventsSince(prev NodeScorecard) []shared.Event {
	if prev.state == StateProvisional && s.state == StateProvisional {
		return nil
	}
	if prev.state == s.state && prev.grade == s.grade && prev.metrics == s.metrics {
		return nil
	}
	header := shared.Header{Version: s.version, At: s.computedAt}
	events := []shared.Event{NodeScorecardUpdated{
		Header: header, NodeID: s.nodeID, Window: s.window, State: s.state, Grade: s.grade,
		Metrics: s.metrics, ComputedAt: s.computedAt,
	}}
	if prev.state != StateGraded || s.state != StateGraded {
		return events
	}
	for _, b := range Breaches(prev.metrics, s.metrics) {
		events = append(events, NodeThresholdBreached{
			Header: header, NodeID: s.nodeID, Window: s.window,
			Metric: b.Metric, Value: b.Value, Threshold: b.Threshold,
		})
	}
	return events
}

// inWindow returns the observations whose instant is after now - window,
// ordered by instant then source id.
func (s *NodeScorecard) inWindow(now time.Time) []Observation {
	from := now.Add(-s.window.Duration())
	out := make([]Observation, 0, len(s.observations))
	for _, o := range s.observations {
		if o.At.After(from) {
			out = append(out, o)
		}
	}
	sortObservations(out)
	return out
}

func indexByID(list []Observation) map[string]Observation {
	out := make(map[string]Observation, len(list))
	for _, o := range list {
		out[o.SourceEventID] = o
	}
	return out
}

func sortObservations(list []Observation) {
	slices.SortFunc(list, func(a, b Observation) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.SourceEventID, b.SourceEventID)
	})
}

// ExpiresAt is the instant at which the oldest observation leaves the window;
// false when there is none.
func (s *NodeScorecard) ExpiresAt() (time.Time, bool) {
	var oldest time.Time
	for _, o := range s.observations {
		if oldest.IsZero() || o.At.Before(oldest) {
			oldest = o.At
		}
	}
	if oldest.IsZero() {
		return time.Time{}, false
	}
	return oldest.Add(s.window.Duration()), true
}

// DueForRecompute reports whether a recompute at now has work: observations
// recorded since the last one, or observations that left the window.
func (s *NodeScorecard) DueForRecompute(now time.Time) bool {
	if s.dirty {
		return true
	}
	expires, ok := s.ExpiresAt()
	return ok && !expires.After(now)
}

// NodeID returns the node.
func (s *NodeScorecard) NodeID() NodeID { return s.nodeID }

// Window returns the window.
func (s *NodeScorecard) Window() Window { return s.window }

// ID returns the scorecardId.
func (s *NodeScorecard) ID() string { return ScorecardID(s.nodeID, s.window) }

// State returns the state.
func (s *NodeScorecard) State() State { return s.state }

// Grade returns the grade; empty while PROVISIONAL.
func (s *NodeScorecard) Grade() Grade { return s.grade }

// GradeLabel returns the wire grade: A to D, or PROVISIONAL.
func (s *NodeScorecard) GradeLabel() string { return GradeLabel(s.state, s.grade) }

// Metrics returns the metrics of the last recompute.
func (s *NodeScorecard) Metrics() Metrics { return s.metrics }

// ComputedAt returns the last recompute instant; zero before the first.
func (s *NodeScorecard) ComputedAt() time.Time { return s.computedAt }

// Version returns the aggregate version.
func (s *NodeScorecard) Version() int64 { return s.version }

// ObservationCount returns the number of observations held.
func (s *NodeScorecard) ObservationCount() int { return len(s.observations) }

// Snapshot returns the persisted form, observations ordered by instant.
func (s *NodeScorecard) Snapshot() Snapshot {
	list := make([]Observation, 0, len(s.observations))
	for _, o := range s.observations {
		list = append(list, o)
	}
	sortObservations(list)
	return Snapshot{
		NodeID: s.nodeID, Window: s.window, State: s.state, Grade: s.grade, Metrics: s.metrics,
		ComputedAt: s.computedAt, Dirty: s.dirty, Version: s.version, Observations: list,
	}
}
