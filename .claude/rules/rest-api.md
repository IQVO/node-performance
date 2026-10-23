---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml` (registered in
`internal/adapters/inbound/http/server.go` `NewRouter`, handlers in `handlers.go`,
wired in `cmd/api/run.go` `buildServer`). Both business routes are reads:

- `GET /scorecards/{nodeId}` -> `Queries.GetScorecard` (`{"nodeId", "scorecards":[7d, 30d]}`;
  404 `not-found` for a node nothing was observed about)
- `GET /scorecards?grade=&window=&limit=&after=` -> `Queries.ListScorecardsByGrade`
  (`{"items":[...], "nextCursor"}`; ordered by `scorecardId`, keyset pagination,
  `limit` 1 to 500, default 100)
- `GET /healthz` (liveness), `GET /readyz` (readiness): not business operations.

vendor-connectivity calls `GET /scorecards/{nodeId}` inside the organization; no
other service calls this API.

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is a kebab-case slug
  (`internal/adapters/inbound/http/problems.go` `problemCatalogue`), plus `title`,
  `status`, `detail`, `instance`. The slugs: `not-found` 404, `validation-failed`
  422 (any `shared.ErrValidation`: grade, window, limit range, cursor, node id),
  `concurrent-update` 409, `malformed-request-query` 400 (unknown parameter, a
  `limit` that is not an integer, NUL), `malformed-request-body` 400 (NUL in a
  body), `method-not-allowed` 405, `dependency-unavailable` 503 (`/readyz`); an
  unmapped error is an opaque 500 `internal-error` whose cause is only logged.
- **No Idempotency-Key**: there is no state-changing route (ADR 0002, A21). The
  `idempotency_keys` table exists for a future write; a new state-changing route
  needs an ADR and the keyed-route middleware of the fleet before it exists.
- **NUL** (U+0000) is rejected before anything else (`nul.go`): 404 in the path, 400
  elsewhere.
- **Handlers hold no business logic**: decode the query, call a use case, encode the view
  (`dto.go` is the wire shape; `scorecardOf`).
- **Auth**: none. REST is unauthenticated, as in the rest of the network
  (`internal/architecture/fitness_test.go` `TestNoAuthMiddlewareReintroduced` keeps it so).
- Unit tests (`http_test.go`) run the real query use cases over the memory test
  doubles; the real wiring on Postgres is proven by `cmd/api/e2e_integration_test.go`.
