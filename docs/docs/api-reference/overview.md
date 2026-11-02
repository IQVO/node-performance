---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "Interfaces of Node Performance (REST, events, MCP, A2A), the endpoint table and the contract conventions."
---

# API overview

:::info Implemented by cmd/api, cmd/mcp and cmd/a2a
`cmd/api` serves every REST route below (`internal/adapters/inbound/http`) on `HTTP_ADDR`
(default `:8080`). The contracts are `apis/openapi.yaml` and `apis/asyncapi.yaml`. There is no
route in the code that the spec does not list: the router registers exactly the two API routes
plus `/healthz` and `/readyz` (`internal/adapters/inbound/http/server.go`). See the
[Quickstart](/docs/overview/quickstart) for real responses.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 0.1.0) | The pages under *Node Performance API* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Server named in the spec: `http://localhost:8080`. Through Kong in the kind cluster: `/api/node-performance` (prefix stripped). |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0007](/docs/adr/0007-mcp-server-adoption), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` and `/` (port 8090; through Kong `/api/mcp/node-performance`). Two read-only tools, no write tool: `get_scorecard`, `list_scorecards_by_grade`. See [MCP tools](/docs/mcp/tools). No auth, by fleet decision. |
| A2A | [ADR 0011](/docs/adr/0011-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/node-performance`), the Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill: `node_performance` (`nodeId`) and `nodes_by_grade` (`grade`, `window`, `limit`, `after`). Domain rejections are answered in the message envelope as problems with the REST slugs, never as JSON-RPC errors. No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Scorecards | `GET /scorecards/{nodeId}` | `getScorecard` | `200`, `{nodeId, scorecards[]}` with the 7-day then the 30-day scorecard; `404 not-found` when no observation was ever recorded for the node |
| Scorecards | `GET /scorecards` | `listScorecardsByGrade` | `200 {items, nextCursor?}`, ordered by `scorecardId` ascending; query `grade` (required), `window`, `limit` (1 to 500, default 100), `cursor`; `400 invalid-limit`, `invalid-cursor` or `unknown-query-parameter`; `422 validation-failed` on a bad value |
| Health | `GET /healthz`, `GET /readyz` (not in the OpenAPI document) | n/a | `200`, or `503` from `/readyz` while draining or when Postgres or Kafka is down; see [Runbook](/docs/operations/runbook#health) |

`/healthz` and `/readyz` are the only HTTP routes not described by `apis/openapi.yaml`; `/a2a` and
`/` on `cmd/a2a` and `/mcp` on `cmd/mcp` belong to their own protocols (below).

## Conventions in the contract

- **Read-only.** Both routes only read; no route takes an `Idempotency-Key` (ADR 0002, A21).
- **Scorecard shape.** `scorecardId` = `nodeId:window` (`7d` or `30d`); `state` is `PROVISIONAL` or
  `GRADED`; `grade` is `A` to `D` or `PROVISIONAL` below 50 observations; `metrics` carries
  `onTimeHandoverRate`, `lateAckRate`, `rejectRate`, `expiryRate`, `promiseKeptRate`,
  `feedFreshnessP95Minutes` and `observations`, plus, when there is data, the informational
  `inboundTimelinessP95Minutes` and `inboundAccuracyRate`; `computedAt` is omitted before the first
  recompute. A metric with an empty denominator reports its best value.
- **Paging.** `limit` 1 to 500 (default 100) and the opaque `cursor` (the previous page's
  `nextCursor`); no `nextCursor` marks the last page. A parameter the operation does not declare
  (a retired `after`, `offset`, `nextToken`) is `400 unknown-query-parameter` ([ADR 0012](/docs/adr/0012-adopt-the-fleet-rest-api-guideline)).
- **Errors** are RFC 9457 `application/problem+json` with `type` equal to a bare slug (not a URL):
  `not-found` (404), `validation-failed` (422), `invalid-limit`, `invalid-cursor`, `unknown-query-parameter`,
  `malformed-request-query` and
  `malformed-request-body` (400, a NUL character), `method-not-allowed` (405),
  `concurrent-update` (409), `dependency-unavailable` (503, `/readyz` only) and `internal-error`
  (500). The OpenAPI document lists `not-found` and `validation-failed`; the others come from the
  adapter (`internal/adapters/inbound/http/problems.go`). An unknown path answers `404 not-found`
  too.
- **Auth**: none. The fleet reverted REST, MCP and A2A auth on 2026-09-11, and
  `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns under
  `internal/adapters/inbound`.
- **Events** are CloudEvents 1.0 in structured mode on Kafka; see the event catalogue.

## MCP and A2A in practice

MCP: `initialize`, then `tools/list` and `tools/call` over `POST /mcp` with
`Accept: application/json, text/event-stream`; errors are tool results with `isError: true`
(`not-found: ...`, `validation-failed: ...`). Details and a captured `tools/list` are on
[MCP tools](/docs/mcp/tools).

A2A: the Agent Card advertises one interface, protocol binding `JSONRPC`, protocol version `1.0`, at
`A2A_AGENT_URL`; capabilities `streaming: false`, `pushNotifications: false`; input and output
modes `application/json` and `text/plain`. Calls need the header `A2A-Version: 1.0` (without it the
agent answers JSON-RPC error `-32009` `VERSION_NOT_SUPPORTED`). A call is `SendMessage` whose first
message part is `{"skill": "...", "arguments": {...}}`, decoded strictly. The answer is one agent
message with a data part `{"skill", "data", "problem"?}` and a one-line text summary. Real exchange
against a node with one observation:

```json
{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m-1","role":"ROLE_USER","parts":[{"text":"{\"skill\":\"node_performance\",\"arguments\":{\"nodeId\":\"node-0f3a91\"}}"}]}}}
```

```text
text part: node-0f3a91: 7d PROVISIONAL (1 observations), 30d PROVISIONAL (1 observations)
```

Rejections are in-band problems with the REST classes: `{"type":"not-found","status":404,...}` for an
unknown node, `{"type":"validation-failed","status":422,...}` for a bad grade, and
`{"type":"unknown-skill","status":400,...}` for a skill the card does not list (a malformed request
is also 400). Source: `internal/adapters/inbound/a2a/skills.go`, `cmd/a2a/router.go`.
