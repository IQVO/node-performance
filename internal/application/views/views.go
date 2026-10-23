// Package views holds the read DTOs of the application: what a query returns,
// never an aggregate.
package views

import (
	"time"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// Metrics is the read form of scorecard.Metrics.
type Metrics struct {
	OnTimeHandoverRate      float64
	LateAckRate             float64
	RejectRate              float64
	ExpiryRate              float64
	PromiseKeptRate         float64
	FeedFreshnessP95Minutes int
	Observations            int
}

// Scorecard is the read form of a NodeScorecard.
type Scorecard struct {
	ScorecardID string
	NodeID      string
	Window      string
	State       string
	// Grade is A, B, C, D or PROVISIONAL.
	Grade   string
	Metrics Metrics
	// ComputedAt is zero until the first recompute.
	ComputedAt time.Time
}

// NodeScorecards is the answer of GetScorecard: both windows of one node.
type NodeScorecards struct {
	NodeID     string
	Scorecards []Scorecard
}

// ScorecardPage is one page of ListScorecardsByGrade.
type ScorecardPage struct {
	Items []Scorecard
	// NextCursor is the scorecardId to pass as `after` for the next page;
	// empty on the last page.
	NextCursor string
}

// MetricsFrom maps the domain metrics.
func MetricsFrom(m scorecard.Metrics) Metrics {
	return Metrics{
		OnTimeHandoverRate: m.OnTimeHandoverRate, LateAckRate: m.LateAckRate, RejectRate: m.RejectRate,
		ExpiryRate: m.ExpiryRate, PromiseKeptRate: m.PromiseKeptRate,
		FeedFreshnessP95Minutes: m.FeedFreshnessP95Minutes, Observations: m.Observations,
	}
}

// ScorecardOf maps a snapshot to its view; the Postgres reader builds views
// from rows with it, so both sides share one mapping.
func ScorecardOf(s scorecard.Snapshot) Scorecard {
	return Scorecard{
		ScorecardID: scorecard.ScorecardID(s.NodeID, s.Window),
		NodeID:      string(s.NodeID),
		Window:      string(s.Window),
		State:       string(s.State),
		Grade:       scorecard.GradeLabel(s.State, s.Grade),
		Metrics:     MetricsFrom(s.Metrics),
		ComputedAt:  s.ComputedAt,
	}
}
