package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/node-performance/internal/adapters/inbound/mcp"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// now is the fixture's fixed recompute instant (whole seconds, so the wire
// format is exactly what the assertions expect).
var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// stubReader is an in-memory ports.ScorecardReader over fixture views: the
// doubles the REST adapter's tests use. It applies the window filter and the
// keyset cursor the way the Postgres reader does, so paging is exercised for
// real; the grade match itself is baked into the fixture pages, keyed by
// grade alone.
type stubReader struct {
	nodes map[string][]views.Scorecard
	pages map[string][]views.Scorecard // key: grade
}

func (r *stubReader) ByNode(_ context.Context, nodeID scorecard.NodeID) ([]views.Scorecard, error) {
	return r.nodes[string(nodeID)], nil
}

func (r *stubReader) ByGrade(_ context.Context, f repository.ScorecardFilter) (views.ScorecardPage, error) {
	// pages is keyed by grade alone; the window filter applies inside the
	// loop, the way the Postgres reader's WHERE clause does.
	all := r.pages[f.Grade]
	items := make([]views.Scorecard, 0, len(all))
	more := false
	for _, v := range all {
		if f.Window != "" && v.Window != string(f.Window) {
			continue
		}
		if f.After != "" && v.ScorecardID <= f.After {
			continue
		}
		if len(items) == f.Limit {
			more = true
			break
		}
		items = append(items, v)
	}
	page := views.ScorecardPage{Items: items}
	if more && len(items) > 0 {
		page.NextCursor = items[len(items)-1].ScorecardID
	}
	return page, nil
}

// gradedA is a GRADED 7-day scorecard of node with grade A.
func gradedA(node string) views.Scorecard {
	return views.Scorecard{
		ScorecardID: node + ":7d", NodeID: node, Window: "7d", State: "GRADED", Grade: "A",
		Metrics: views.Metrics{
			OnTimeHandoverRate: 0.98, LateAckRate: 0.01, RejectRate: 0.005, ExpiryRate: 0.005,
			PromiseKeptRate: 0.97, FeedFreshnessP95Minutes: 38, Observations: 412,
		},
		ComputedAt: now,
	}
}

// provisional is a PROVISIONAL 30-day scorecard of node (below the 50
// observations minimum: no grade yet).
func provisional(node string) views.Scorecard {
	return views.Scorecard{
		ScorecardID: node + ":30d", NodeID: node, Window: "30d", State: "PROVISIONAL", Grade: "PROVISIONAL",
		Metrics:    views.Metrics{Observations: 12},
		ComputedAt: now,
	}
}

// newHarness wires the real MCP server to the stub reader and connects an
// in-memory client, the way cmd/mcp wires them (minus Postgres).
func newHarness(t *testing.T, reader *stubReader) *harness {
	t.Helper()
	queries := usecases.Queries{Reader: reader}
	deps := inboundmcp.Deps{GetScorecard: queries, ListScorecards: queries}

	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &harness{session: session}
}

type harness struct {
	session *sdk.ClientSession
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func (h *harness) call(t *testing.T, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := h.session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := h.call(t, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func (h *harness) failWith(t *testing.T, name string, args map[string]any, want string) {
	t.Helper()
	res := h.call(t, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
