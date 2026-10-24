package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
)

// The use-case contracts the tools call: the SAME interfaces the REST adapter
// defines (internal/adapters/inbound/http/handlers.go). usecases.Queries
// satisfies both; the tests substitute doubles. No write use case is
// reachable from here (docs/adr/0007).
type (
	getScorecard interface {
		GetScorecard(ctx context.Context, nodeID string) (views.NodeScorecards, error)
	}
	listScorecards interface {
		ListScorecardsByGrade(ctx context.Context, in usecases.ListByGradeInput) (views.ScorecardPage, error)
	}
)

// Deps is everything the MCP tools need, injected by the composition root
// (cmd/mcp). It carries the SAME read use cases the REST adapter serves
// (GET /scorecards/{nodeId} and GET /scorecards); the adapter never
// constructs an outbound adapter itself, and it holds no write use case at
// all. The write path (observation intake through the Kafka consumers and
// the recompute scheduler) stays in cmd/api.
type Deps struct {
	GetScorecard   getScorecard
	ListScorecards listScorecards
}

// --- inputs -------------------------------------------------------------------

type nodeInput struct {
	NodeID string `json:"nodeId" jsonschema:"the node whose scorecards to read, e.g. N-0042; both the 7-day and the 30-day window are returned"`
}

type listScorecardsInput struct {
	Grade  string `json:"grade" jsonschema:"the current grade to list: A, B, C, D or PROVISIONAL (required)"`
	Window string `json:"window,omitempty" jsonschema:"optional window filter, 7d or 30d; omitted means both windows"`
	Limit  int    `json:"limit,omitempty" jsonschema:"optional page size, 1..500; omitted or 0 means the default page size of 100"`
	After  string `json:"after,omitempty" jsonschema:"the optional cursor returned as nextCursor by the previous page; omitted means the first page"`
}

// --- outputs (the REST bodies, camelCase like the REST contract) --------------

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

// --- mapping ------------------------------------------------------------------

// instant formats an instant as RFC 3339 UTC (the REST contract's format);
// the zero time is "" (omitted).
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

func toScorecard(v views.Scorecard) scorecardView {
	return scorecardView{
		ScorecardID: v.ScorecardID, NodeID: v.NodeID, Window: v.Window, State: v.State, Grade: v.Grade,
		Metrics: toMetrics(v.Metrics), ComputedAt: instant(v.ComputedAt),
	}
}

func toNodeScorecards(got views.NodeScorecards) nodeScorecardsView {
	out := nodeScorecardsView{NodeID: got.NodeID, Scorecards: make([]scorecardView, 0, len(got.Scorecards))}
	for _, v := range got.Scorecards {
		out.Scorecards = append(out.Scorecards, toScorecard(v))
	}
	return out
}

func toScorecardPage(page views.ScorecardPage) scorecardPageView {
	out := scorecardPageView{Items: make([]scorecardView, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, v := range page.Items {
		out.Items = append(out.Items, toScorecard(v))
	}
	return out
}

// --- handlers -----------------------------------------------------------------

func (d Deps) getScorecard(ctx context.Context, in nodeInput) (nodeScorecardsView, error) {
	got, err := d.GetScorecard.GetScorecard(ctx, in.NodeID)
	if err != nil {
		return nodeScorecardsView{}, mapError(err)
	}
	return toNodeScorecards(got), nil
}

func (d Deps) listScorecardsByGrade(ctx context.Context, in listScorecardsInput) (scorecardPageView, error) {
	page, err := d.ListScorecards.ListScorecardsByGrade(ctx, usecases.ListByGradeInput{
		Grade: in.Grade, Window: in.Window, Limit: in.Limit, After: in.After,
	})
	if err != nil {
		return scorecardPageView{}, mapError(err)
	}
	return toScorecardPage(page), nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the two read-only tools, one per read operation of
// apis/openapi.yaml. Every tool is annotated read-only, idempotent and
// closed-world; TestToolSurface pins the set and fails the build on any
// write-verb name (docs/adr/0007).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	addTool(server, &mcp.Tool{
		Name: "get_scorecard",
		Description: "Read both scorecards of a node: the 7-day and the 30-day window, in that order, each with its " +
			"state (PROVISIONAL or GRADED), its grade (A, B, C, D, or PROVISIONAL below the observation minimum of 50), " +
			"its metrics (onTimeHandoverRate, lateAckRate, rejectRate, expiryRate, promiseKeptRate, " +
			"feedFreshnessP95Minutes, observations) and computedAt, omitted before the first recompute. Read-only; an " +
			"unknown node fails with not-found and a malformed node id with validation-failed.",
		Annotations: readOnly,
	}, d.getScorecard)

	addTool(server, &mcp.Tool{
		Name: "list_scorecards_by_grade",
		Description: "List the scorecards whose current grade matches, ordered by scorecardId ascending, one page at " +
			"a time: grade is required (A, B, C, D or PROVISIONAL), the window filter (7d or 30d), the page size " +
			"(1..500, default 100) and the cursor are optional. Pass the returned nextCursor as after to get the next " +
			"page; it is absent on the last page. Read-only; a bad grade, window or limit fails with validation-failed.",
		Annotations: readOnly,
	}, d.listScorecardsByGrade)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
