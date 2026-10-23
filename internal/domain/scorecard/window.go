// Package scorecard is the NodeScorecard aggregate: the rolling-window
// performance of one node, built only from observed facts (ADR 0002).
//
// States: PROVISIONAL (initial, below MinObservations) and GRADED. Commands:
// Open (the first observation of a node), RecordObservation and Recompute.
// Invariants: rates are in [0,1] (ErrRateOutOfRange), a grade requires
// observations >= MinObservations (ErrInsufficientObservations), an observation
// is counted once (ErrDuplicateObservation).
package scorecard

import (
	"errors"
	"strings"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

var (
	// ErrRateOutOfRange is raised by NewMetrics for a rate outside [0,1] (or
	// NaN), a negative freshness or a negative observation count.
	ErrRateOutOfRange = errors.New("rate out of range")
	// ErrInsufficientObservations is raised by GradeFor below MinObservations.
	ErrInsufficientObservations = errors.New("insufficient observations")
	// ErrDuplicateObservation is raised by RecordObservation when the source
	// event id was already counted.
	ErrDuplicateObservation = errors.New("duplicate observation")
)

// Window is a rolling window of a scorecard.
type Window string

const (
	// Window7d is the 7-day window; it feeds alerts only (G20, E18).
	Window7d Window = "7d"
	// Window30d is the 30-day window; consumers decide on it (G20, E18).
	Window30d Window = "30d"
)

// Windows returns every window, shortest first.
func Windows() []Window { return []Window{Window7d, Window30d} }

// ParseWindow returns the window named s, or an error wrapping ErrValidation.
func ParseWindow(s string) (Window, error) {
	w := Window(s)
	if !w.Valid() {
		return "", shared.Invalid("window must be 7d or 30d")
	}
	return w, nil
}

// Valid reports whether w is one of the two windows.
func (w Window) Valid() bool { return w == Window7d || w == Window30d }

// Duration returns the length of the window; zero for an invalid one.
func (w Window) Duration() time.Duration {
	switch w {
	case Window7d:
		return 7 * 24 * time.Hour
	case Window30d:
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}

// NodeID is the id of a node, minted by node-topology.
type NodeID string

// NewNodeID validates s: a valid aggregate id that does not contain ':' (the
// separator of the scorecardId).
func NewNodeID(s string) (NodeID, error) {
	if !shared.ValidID(s) || strings.Contains(s, ":") {
		return "", shared.Invalid("nodeId must be 1 to %d characters from A-Z a-z 0-9 . _ -", shared.MaxID)
	}
	return NodeID(s), nil
}

// ScorecardID returns nodeId + ':' + window.
func ScorecardID(n NodeID, w Window) string { return string(n) + ":" + string(w) }
