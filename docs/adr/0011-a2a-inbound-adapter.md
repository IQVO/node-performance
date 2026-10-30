# ADR 0011: an A2A inbound adapter

## Status

Accepted (2026-10-30).

## Context

Three channels leave and enter a bounded context of this fleet, and they must not overlap:

- **Kafka + CloudEvents carry facts.** node-performance publishes `NodeScorecardUpdated` and
  `NodeThresholdBreached` through the transactional outbox (ADR 0004, 0005) and consumes thirteen event types from five
  producers (ADR 0003).
- **MCP carries an agent's tools.** The read-only MCP server (ADR 0007) lets an agent read scorecards.
- **A2A carries the delegation of a goal between agents**, and nothing else: never a data feed, never a sync of another
  context's state.

Other contexts of the fleet already answer Agent2Agent delegations; node-performance did not, so an agent that wanted
to ask "how is this node doing" or "which nodes sit at grade D" had to use the MCP tools or the REST API directly. This
decision adds the A2A way in, as a third driving adapter over the SAME read use cases.

## Decision

- **A third driving adapter, a separate binary.** `internal/adapters/inbound/a2a` is built on the official A2A Go SDK
  (`github.com/a2aproject/a2a-go/v2` v2.7.0) and served as JSON-RPC 2.0 over HTTP by `cmd/a2a`: the endpoint is mounted
  at `/` and `/a2a`, the Agent Card at `/.well-known/agent-card.json`, plus an open `/healthz`. It is a third deployable
  beside `cmd/api` and `cmd/mcp`; the Helm chart gets an a2a Deployment, Service and (off by default) Gateway API
  HTTPRoute, all behind `a2a.enabled` (default off). The agent is a **message-only agent** (no Task lifecycle, no
  streaming): both skills are transactional request/response reads.
- **READ-ONLY, because the MCP charter is.** A skill may change state only if the context's MCP surface already offers
  that write. ADR 0007 makes the MCP surface read-only (two read tools, `TestToolSurface` fails the build on a
  write-verb name), so this agent is read-only and widens nothing. Recording observations, the recompute and the
  outbox relay stay on the event consumers and the scheduler of `cmd/api`; no write use case is reachable from the
  A2A adapter or from `cmd/a2a`.
- **Two curated, goal-shaped skills**, each the goal of one MCP read tool and served by the same use case
  (`usecases.Queries`) as REST and MCP:
  - `node_performance` (read): `{nodeId}`; both scorecards of one node, 7-day then 30-day (state, grade, metrics,
    `computedAt` omitted before the first recompute). From `get_scorecard`.
  - `nodes_by_grade` (read): `{grade, window?, limit?, after?}`; one page of the scorecards whose current grade
    matches, with `nextCursor` while more exist. From `list_scorecards_by_grade`.

  Not one skill per endpoint and not more than two; the card test pins the set, and a new skill is a reviewed act
  (an amendment of this ADR).
- **Input contract.** The first message part carries `{"skill":"<id>","arguments":{...}}` as a data part, or a text part
  holding the same JSON. Decoding is strict (`DisallowUnknownFields`, trailing data refused), so a misspelled
  argument such as `windw` cannot silently widen a read to both windows. Argument names and results are lowerCamelCase
  like the REST contract.
- **Answers, and problems in-band.** Every answer is one agent message with a data part (the envelope
  `{skill, data | problem}`) and a text part (a one-line summary), bound to the caller's `contextId`. Domain rejections
  and bad requests are ANSWERED as RFC 7807-shaped `problem`s inside the envelope, never as JSON-RPC errors, with the
  same classification and `type` slug as the REST adapter (`internal/adapters/inbound/http/problems.go`): `404
  not-found` (a node nothing was observed about), `422 validation-failed` (a value the domain refuses: node id, grade,
  window, limit, cursor), `500 internal-error` (anything else; the raw text, which may carry infrastructure detail, is
  logged and never echoed). The adapter's own refusals are `400 malformed-request-body` (not JSON, an unknown or
  mistyped argument, trailing data) and `400 unknown-skill`. The REST `409 concurrent-update` belongs to a write path
  and the `503 dependency-unavailable` to the readiness probe; neither exists on a read skill.
- **No auth, no sibling calls.** The Agent Card declares no security schemes: A2A endpoints are unauthenticated by the
  same fleet-wide decision as REST and MCP (the 2026-09-11 revert; `TestNoAuthMiddlewareReintroduced`), and access
  control is the in-cluster ClusterIP boundary. The agent reads this context's own tables only, calls no other
  context and contains no outbound A2A client. The facts between contexts stay on Kafka.
- **Boot.** `cmd/a2a` clones `cmd/mcp`'s composition (generated from it by asserted replacements, so a layout
  difference fails loudly): `DATABASE_URL` is mandatory (no in-memory mode), the idempotent embedded migrations run on
  start (`MIGRATIONS_DATABASE_URL` for a direct DSN behind PgBouncer), and the Postgres dial is wrapped in the shared
  boot retry. `A2A_AGENT_URL` is mandatory too: the absolute `http(s)` URL the Agent Card advertises, refused at boot
  (before telemetry, any connection and the listener) when missing, relative or schemeless. `A2A_ADDR` defaults to
  `:8093`. The chart defaults `A2A_AGENT_URL` to `http://<release>-a2a.<namespace>.svc.cluster.local:8093/` and
  `a2a.agentUrl` overrides it. The binary has no Kafka, consumer-group or `EVENT_PUBLISHER` environment, and the chart
  tests assert those variables are ABSENT from its Deployment.
- **Observability.** One OTel span per skill run, `a2a.skill <id>` with the `a2a.skill` attribute (an error status on a
  problem answer), mirroring the `mcp.tool` spans; the HTTP router has the same fleet middleware as `cmd/mcp`
  (request id, server span named by route, request-duration histogram, trace-correlated request log, ADR 0010);
  the OTel service name defaults to `node-performance-a2a`.
- **Fitness.** `internal/architecture/a2a_fitness_test.go`: the adapter depends on application and domain only and nothing
  else depends on it; an ALLOWLIST sensor (`usecases.Queries` and `usecases.ListByGradeInput`, `ports.ErrNotFound`)
  over the adapter and `cmd/a2a` sources, so a write use case, a unit of work, a publisher, Kafka, the outbox, the
  in-memory adapters or an `a2aclient` import is refused by default; `Deps` is pinned to the two read use cases; a
  bite test proves the sensor flags each shape in code (not in a comment).
- **Tests.** The skills are exercised over the real JSON-RPC wire (`httptest` plus `a2aclient.NewFromCard`, with a
  test-local envelope struct so contract drift fails the suite); an argument-validation table, the in-band problem
  classes, the card shape and truthfulness (every example parses under the executor's strict decoder), the span
  attribute, the `cmd/a2a` boot refusals, router and wiring (including that the migrations step uses
  `MIGRATIONS_DATABASE_URL`, not `DATABASE_URL`), and an integration test (`make integration`, testcontainers Postgres)
  that proves the binary answers over the wire what the write path persisted and leaves the outbox untouched.

## Consequences

- An agent can delegate "how is N-0042 doing" or "who holds grade D" to this context through its Agent Card, with the
  answers shaped like the REST bodies and the failures classified like REST's.
- The agent exposes exactly what the MCP surface exposes; widening it (a write skill, a third read) means amending
  this ADR, ADR 0007 and the tests that pin the surface together.
- A third process to run (`a2a.enabled`, default off): the chart gains an a2a Deployment and Service, the image gains
  `/app/a2a` (the Dockerfile builds every `cmd/*`), and port 8093 joins 8080 (api) and 8090 (mcp).
- Like the MCP surface, the skills do not expose the informational inbound timeliness and accuracy metrics of ADR 0009
  (the REST bodies carry them; `cmd/mcp` wires `usecases.Queries` without the inbound reader). Exposing them over MCP and
  A2A together is a separate decision.
- Grading stays with the service itself: the write path (consumers, scheduler, transactional outbox) never moves behind
  the A2A surface.
