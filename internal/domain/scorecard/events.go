package scorecard

import (
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

// EntityScorecard is the entity segment of the CloudEvents type of both events.
const EntityScorecard = "scorecard"

// NodeScorecardUpdated is raised by Recompute when the state, grade or metrics
// changed (ADR 0002, A13). Integration event, topic .events.
type NodeScorecardUpdated struct {
	shared.Header
	NodeID  NodeID
	Window  Window
	State   State
	Grade   Grade
	Metrics Metrics
	// ComputedAt is the recompute instant.
	ComputedAt time.Time
}

// EventName returns "NodeScorecardUpdated".
func (NodeScorecardUpdated) EventName() string { return "NodeScorecardUpdated" }

// Entity returns "scorecard".
func (NodeScorecardUpdated) Entity() string { return EntityScorecard }

// SubjectID returns the scorecardId.
func (e NodeScorecardUpdated) SubjectID() string { return ScorecardID(e.NodeID, e.Window) }

// AnalyticsOnly is false: consumers act on this event.
func (NodeScorecardUpdated) AnalyticsOnly() bool { return false }

// GradeLabel is the wire grade: A to D, or PROVISIONAL.
func (e NodeScorecardUpdated) GradeLabel() string { return GradeLabel(e.State, e.Grade) }

// NodeThresholdBreached is raised by Recompute, one per metric that crossed its
// threshold downward. Analytics only, topic .analytics.
type NodeThresholdBreached struct {
	shared.Header
	NodeID    NodeID
	Window    Window
	Metric    MetricName
	Value     float64
	Threshold float64
}

// EventName returns "NodeThresholdBreached".
func (NodeThresholdBreached) EventName() string { return "NodeThresholdBreached" }

// Entity returns "scorecard".
func (NodeThresholdBreached) Entity() string { return EntityScorecard }

// SubjectID returns the scorecardId.
func (e NodeThresholdBreached) SubjectID() string { return ScorecardID(e.NodeID, e.Window) }

// AnalyticsOnly is true: the model has no in-organization consumer.
func (NodeThresholdBreached) AnalyticsOnly() bool { return true }
