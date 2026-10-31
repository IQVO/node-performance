---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: "The two read-only MCP tools of the Node Performance server: input fields, annotations, results and errors, captured from a live tools/list."
---

# MCP tools

`cmd/mcp` is a separate, read-only deployable ([ADR 0007](/docs/adr/0007-mcp-server-adoption)).
It speaks MCP over **Streamable HTTP only** (no stdio, no SSE) on `MCP_ADDR`
(default `:8090`), served at `/mcp` and at `/`, plus an open `GET /healthz`. In
the kind cluster it sits behind Kong at `/api/mcp/node-performance`. It is
**unauthenticated**, like REST and A2A: access control is the in-cluster
ClusterIP boundary.

Source: `cmd/mcp/main.go` (composition root), `cmd/mcp/router.go`,
`internal/adapters/inbound/mcp`. The binary wires only the two read use cases
`GetScorecard` and `ListScorecardsByGrade` over the Postgres reader. It never
starts the outbox relay, the consumers or the scheduler and never dials Kafka,
so it cannot publish and it cannot change a scorecard.

The server identifies itself as `node-performance-mcp` (version `1.0.0`),
protocol `2025-06-18`. The table below was produced by a real `initialize` +
`tools/list` against the binary built from this branch.

| Tool | Input | Annotations | Returns |
| --- | --- | --- | --- |
| `get_scorecard` | `nodeId` (string, required) | `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false` | Both scorecards of the node, the 7-day then the 30-day one |
| `list_scorecards_by_grade` | `grade` (string, required: `A`, `B`, `C`, `D`, `PROVISIONAL`); `window` (string, optional: `7d` or `30d`); `limit` (integer, optional: 1 to 500, default 100); `after` (string, optional: the previous page's `nextCursor`) | `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false` | `{items, nextCursor?}` ordered by `scorecardId` ascending |

Both tools set `additionalProperties: false` on input and output. There is no
write tool: every tool is read-only.

## `get_scorecard`

Input: `{"nodeId": "node-0f3a91"}`. The structured result matches the REST
`NodeScorecards` shape: `nodeId` and `scorecards`, each with `scorecardId`,
`nodeId`, `window`, `state` (`PROVISIONAL` or `GRADED`), `grade`, `metrics`
(`onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`,
`promiseKeptRate`, `feedFreshnessP95Minutes`, `observations`) and `computedAt`
(omitted before the first recompute). A real call, after the one `PromiseKept`
event of the [Quickstart](/docs/overview/quickstart):

```json
{"nodeId":"node-0f3a91","scorecards":[{"computedAt":"2026-10-31T22:25:08Z","grade":"PROVISIONAL","metrics":{"expiryRate":0,"feedFreshnessP95Minutes":0,"lateAckRate":0,"observations":1,"onTimeHandoverRate":1,"promiseKeptRate":1,"rejectRate":0},"nodeId":"node-0f3a91","scorecardId":"node-0f3a91:7d","state":"PROVISIONAL","window":"7d"},{"...":"the 30d scorecard"}]}
```

## `list_scorecards_by_grade`

Input: `{"grade": "PROVISIONAL", "window": "30d", "limit": 10}`. The result is the
REST `ScorecardList`: `items` and, when more remain, `nextCursor` (pass it as
`after`).

## Errors

A failed call is a tool result with `isError: true` and one text content
`<problem-slug>: <message>`, the same slugs the REST adapter uses. Real
answers:

| Call | Result text |
| --- | --- |
| `get_scorecard` `{"nodeId":"nope"}` | `not-found: not found: no scorecard for node nope` |
| `list_scorecards_by_grade` `{"grade":"Z"}` | `validation-failed: validation failed: grade must be one of A, B, C, D, PROVISIONAL` |

## Not offered

- No tool that records an observation, recomputes or publishes: facts arrive only
  as CloudEvents on Kafka and grades are computed by `cmd/api`.
- The `inboundTimelinessP95Minutes` and `inboundAccuracyRate` metrics of
  [ADR 0009](/docs/adr/0009-inbound-timeliness-and-accuracy-metrics) are
  omitted from the tool output schema, which is `additionalProperties: false`
  over the seven graded metrics; the REST read carries them when present.
