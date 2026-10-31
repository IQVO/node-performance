// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the agent ecosystem as a third driving adapter over the same
// application-layer read use cases the REST and MCP adapters use. It is built
// on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0 over
// HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0011-a2a-inbound-adapter.md, A2A is the DELEGATION channel: an
// agent client sends a goal-shaped skill request as a structured message part
// and this agent answers from its own state through its own use cases. It is
// not a data-sync channel: facts between contexts still travel exclusively as
// CloudEvents on Kafka (ADR 0004, 0005).
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0007): both
// skills read scorecards. Recording observations and the recompute stay on the
// event consumers and the scheduler of cmd/api, so nothing here ever reaches
// the outbox. This package never calls another bounded context and contains
// no outbound A2A client.
//
// Like the MCP adapter, this package depends inward on the application layer
// (use cases) and the domain only, never on an outbound adapter. The
// composition root (cmd/a2a) wires the Postgres reader into the query use
// cases. Domain structs never leak across the message boundary. There is no
// auth of any kind (fleet-wide revert 2026-09-11).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
)

// The use-case contracts the skills call: the SAME interfaces the REST and
// MCP adapters define. usecases.Queries satisfies both; the tests substitute
// doubles. No write use case is reachable from here (docs/adr/0011).
type (
	getScorecard interface {
		GetScorecard(ctx context.Context, nodeID string) (views.NodeScorecards, error)
	}
	listScorecards interface {
		ListScorecardsByGrade(ctx context.Context, in usecases.ListByGradeInput) (views.ScorecardPage, error)
	}
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the agent clients can actually
// dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the SAME read use cases the REST and MCP adapters serve; the
// adapter never constructs an outbound adapter itself and holds no write use
// case at all.
type Deps struct {
	// GetScorecard is the existing read use case, reused unchanged: both
	// scorecards (7-day and 30-day) of one node.
	GetScorecard getScorecard
	// ListScorecards is the existing read use case, reused unchanged: one page
	// of the scorecards whose current grade matches.
	ListScorecards listScorecards
}

// Card builds this agent's Agent Card (ADR 0011). The surface is two curated,
// goal-shaped READ skills derived from the MCP tools get_scorecard and
// list_scorecards_by_grade (ADR 0007): this context's MCP charter is entirely
// read-only, so the agent is read-only too and never widens what the context
// exposes. The card declares no security schemes on purpose: this surface is
// unauthenticated by the same fleet-wide decision as REST and MCP; schemes
// are added here, not in a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "node-performance",
		Description: "Node Performance agent for the retail network (read-only): answers how a node is performing, from the two scorecards (7-day and 30-day window) this context grades from observed facts only (on-time handover, acknowledgement latency, rejections, expiry, promise-kept rate and inventory feed freshness), and which nodes currently sit at a given grade (A, B, C, D or PROVISIONAL below 50 observations). It never changes data: observations are recorded by the event consumers and grades are computed by the service itself.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/node-performance",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillNodePerformance,
				Name:        "How is this node performing",
				Description: "Read both scorecards of one node by nodeId: the 7-day and the 30-day window, in that order, each with its state (PROVISIONAL or GRADED), its grade (A, B, C, D, or PROVISIONAL below the observation minimum of 50), its metrics (onTimeHandoverRate, lateAckRate, rejectRate, expiryRate, promiseKeptRate, feedFreshnessP95Minutes, observations) and computedAt, omitted before the first recompute. Answers a 404 problem for a node nothing was observed about and a 422 problem for a malformed nodeId. Persists nothing.",
				Tags:        []string{"node-performance", "scorecard", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"node_performance","arguments":{"nodeId":"N-0042"}}`,
				},
			},
			{
				ID:          skillNodesByGrade,
				Name:        "Which nodes sit at this grade",
				Description: "List the scorecards whose current grade matches, ordered by scorecardId ascending, one page at a time: grade is required (A, B, C, D or PROVISIONAL); the window filter (7d or 30d), the page size (limit, 1..500, default 100) and the cursor are optional. Pass the returned nextCursor as after to get the next page; it is absent on the last page. Answers a 422 problem for a bad grade, window, limit or cursor. Persists nothing.",
				Tags:        []string{"node-performance", "scorecard", "grade", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"nodes_by_grade","arguments":{"grade":"D"}}`,
					`{"skill":"nodes_by_grade","arguments":{"grade":"A","window":"7d","limit":50}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (ADR 0011), mirroring ADR 0007's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
