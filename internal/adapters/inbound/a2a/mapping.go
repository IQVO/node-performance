package a2a

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/node-performance/internal/application/views"
)

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope. Type
// is the same slug the REST adapter carries in its RFC 9457 "type" field
// (internal/adapters/inbound/http/problems.go), so one error vocabulary spans
// the surfaces.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

// problemMalformed is a request the adapter itself refuses (not JSON, an
// unknown or mistyped field, trailing data): the REST adapter's 400
// malformed-request-body class.
func problemMalformed(detail string) *problem {
	return &problem{Type: "malformed-request-body", Title: "Malformed request body", Detail: detail, Status: http.StatusBadRequest}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: http.StatusBadRequest}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	NodePerformance *nodeScorecardsView `json:"nodePerformance,omitempty"`
	NodesByGrade    *scorecardPageView  `json:"nodesByGrade,omitempty"`
	summary         string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// --- result DTOs (the REST bodies, camelCase like the REST contract) ----------

type metricsView struct {
	OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
	LateAckRate             float64 `json:"lateAckRate"`
	RejectRate              float64 `json:"rejectRate"`
	ExpiryRate              float64 `json:"expiryRate"`
	PromiseKeptRate         float64 `json:"promiseKeptRate"`
	FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
	Observations            int     `json:"observations"`
}

type scorecardView struct {
	ScorecardID string      `json:"scorecardId"`
	NodeID      string      `json:"nodeId"`
	Window      string      `json:"window"`
	State       string      `json:"state"`
	Grade       string      `json:"grade"`
	Metrics     metricsView `json:"metrics"`
	// ComputedAt is omitted before the first recompute, like the REST body.
	ComputedAt string `json:"computedAt,omitempty"`
}

type nodeScorecardsView struct {
	NodeID     string          `json:"nodeId"`
	Scorecards []scorecardView `json:"scorecards"`
}

type scorecardPageView struct {
	Items      []scorecardView `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// instant formats an instant as RFC 3339 UTC (the REST contract's format); the
// zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func toMetrics(m views.Metrics) metricsView {
	return metricsView{
		OnTimeHandoverRate: m.OnTimeHandoverRate, LateAckRate: m.LateAckRate, RejectRate: m.RejectRate,
		ExpiryRate: m.ExpiryRate, PromiseKeptRate: m.PromiseKeptRate,
		FeedFreshnessP95Minutes: m.FeedFreshnessP95Minutes, Observations: m.Observations,
	}
}

func toScorecards(items []views.Scorecard) []scorecardView {
	out := make([]scorecardView, 0, len(items))
	for _, v := range items {
		out = append(out, scorecardView{
			ScorecardID: v.ScorecardID, NodeID: v.NodeID, Window: v.Window, State: v.State, Grade: v.Grade,
			Metrics: toMetrics(v.Metrics), ComputedAt: instant(v.ComputedAt),
		})
	}
	return out
}

func toNodeScorecards(got views.NodeScorecards) nodeScorecardsView {
	return nodeScorecardsView{NodeID: got.NodeID, Scorecards: toScorecards(got.Scorecards)}
}

func toScorecardPage(page views.ScorecardPage) scorecardPageView {
	return scorecardPageView{Items: toScorecards(page.Items), NextCursor: page.NextCursor}
}

// summarizeNode is the one-line text form of a node's scorecards.
func summarizeNode(n nodeScorecardsView) string {
	parts := make([]string, 0, len(n.Scorecards))
	for _, s := range n.Scorecards {
		parts = append(parts, fmt.Sprintf("%s %s (%d observations)", s.Window, s.Grade, s.Metrics.Observations))
	}
	return fmt.Sprintf("%s: %s", n.NodeID, strings.Join(parts, ", "))
}

// summarizePage is the one-line text form of one page of scorecards.
func summarizePage(grade string, p scorecardPageView) string {
	ids := make([]string, 0, len(p.Items))
	for _, s := range p.Items {
		ids = append(ids, s.ScorecardID)
	}
	line := fmt.Sprintf("%d scorecards at grade %s", len(p.Items), grade)
	if len(ids) > 0 {
		line += ": " + strings.Join(ids, ", ")
	}
	if p.NextCursor != "" {
		line += "; more available, next cursor " + p.NextCursor
	}
	return line
}
