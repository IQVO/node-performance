// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0007-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads scorecard data (get_scorecard, list_scorecards_by_grade);
// recording observations and the recompute stay on the event consumers and
// the scheduler of cmd/api, and governance_test.go fails the build on a
// write-verb tool name. This package depends inward on the application layer
// and the domain only, never on an outbound adapter or the REST adapter, and
// nothing else may depend on it (internal/architecture's
// TestMCPAdapterDependencyRule). The composition root (cmd/mcp) wires the
// Postgres reader into the query use cases.
// There is no auth of any kind (fleet-wide revert 2026-09-11;
// TestNoAuthMiddlewareReintroduced in internal/architecture).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "node-performance-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Node performance of the retail network (read-only): read both scorecards (the 7-day and the " +
	"30-day window) of one node with get_scorecard, or page through every scorecard whose current grade matches " +
	"(A, B, C, D or PROVISIONAL) with list_scorecards_by_grade, optionally restricted to one window. A scorecard " +
	"grades a node from observed facts only (on-time handover, acknowledgement latency, rejections, expiry, " +
	"promise-kept rate and inventory feed freshness); below 50 observations it is PROVISIONAL and carries no grade. " +
	"Nothing here changes data: observations are recorded by the event consumers and grades are computed by the " +
	"service itself."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
