package http

import (
	"time"

	"github.com/claudioed/node-performance/internal/application/views"
)

// The wire shapes of apis/openapi.yaml (camelCase JSON).

type metricsDTO struct {
	OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
	LateAckRate             float64 `json:"lateAckRate"`
	RejectRate              float64 `json:"rejectRate"`
	ExpiryRate              float64 `json:"expiryRate"`
	PromiseKeptRate         float64 `json:"promiseKeptRate"`
	FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
	Observations            int     `json:"observations"`
}

type scorecardDTO struct {
	ScorecardID string     `json:"scorecardId"`
	NodeID      string     `json:"nodeId"`
	Window      string     `json:"window"`
	State       string     `json:"state"`
	Grade       string     `json:"grade"`
	Metrics     metricsDTO `json:"metrics"`
	// ComputedAt is omitted before the first recompute.
	ComputedAt *string `json:"computedAt,omitempty"`
}

type nodeScorecardsDTO struct {
	NodeID     string         `json:"nodeId"`
	Scorecards []scorecardDTO `json:"scorecards"`
}

type scorecardListDTO struct {
	Items      []scorecardDTO `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

func scorecardOf(v views.Scorecard) scorecardDTO {
	d := scorecardDTO{
		ScorecardID: v.ScorecardID, NodeID: v.NodeID, Window: v.Window, State: v.State, Grade: v.Grade,
		Metrics: metricsDTO{
			OnTimeHandoverRate: v.Metrics.OnTimeHandoverRate, LateAckRate: v.Metrics.LateAckRate,
			RejectRate: v.Metrics.RejectRate, ExpiryRate: v.Metrics.ExpiryRate, PromiseKeptRate: v.Metrics.PromiseKeptRate,
			FeedFreshnessP95Minutes: v.Metrics.FeedFreshnessP95Minutes, Observations: v.Metrics.Observations,
		},
	}
	if !v.ComputedAt.IsZero() {
		at := v.ComputedAt.UTC().Format(time.RFC3339)
		d.ComputedAt = &at
	}
	return d
}

func scorecardsOf(list []views.Scorecard) []scorecardDTO {
	out := make([]scorecardDTO, len(list))
	for i, v := range list {
		out[i] = scorecardOf(v)
	}
	return out
}
