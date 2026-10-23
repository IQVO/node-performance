---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the
invariant it relies on validates nothing and gets bypassed.

The worked example is `GET /scorecards?grade=`:
`internal/application/usecases/queries.go` `ListScorecardsByGrade` ->
`internal/adapters/inbound/http/handlers.go` `handleListScorecards`. Both routes of
this service are reads; a write would follow the same order and needs an ADR first
(no state-changing route exists, ADR 0002 A21).

## 1. Domain first

Does the rule already exist in `internal/domain/scorecard/`? A handler holds no
business logic. A new rule goes into the aggregate with a table-driven test in the
same package (`scorecard_test.go`, `grade_test.go`, ...) and a named `Err...`
variable. Pure Go: no I/O, `now` passed in. Then re-run the mutation gate for that
package (`gremlins unleash ./internal/domain/scorecard`).

## 2. Application: the use case

Add it to `internal/application/usecases/` (`queries.go` for reads; `intake.go` and
`recompute.go` for commands). A command embeds `writer` (`writer.go`): `transact`
runs load, domain call, `Save` and the outbox append in ONE `ports.UnitOfWork` and
retries on `ports.ErrConcurrentUpdate`. A query holds only its read port and returns
a view from `internal/application/views`. A new repository or reader method goes into
`internal/application/ports/ports.go` (interfaces only; filters in
`internal/application/repository`), is implemented in
`internal/adapters/outbound/postgres/` AND `internal/adapters/outbound/memory/`, and
extends the shared suite in `internal/adapters/outbound/repocontract/` so both run
it. Unit-test the use case in `internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- the response shape goes into `dto.go` (camelCase, optional fields omitted);
- add the use-case interface to `handlers.go`, the field to `Server` and the route
  to `NewRouter` in `server.go` (`NewRouter` refuses a server missing a use case);
- map a new error in `problems.go` `problemCatalogue` (kebab-case slug, the status the
  OpenAPI documents) and add a row to `TestErrorsMapToTheProblemCatalogue`;
- wire the use case in `cmd/api/run.go` `buildServer`.

## 4. Contract: apis/openapi.yaml

Describe the operation, its parameters, the success body and every problem it can
return, then lint:
`npx --yes @stoplight/spectral-cli lint apis/openapi.yaml --ruleset .spectral.yaml`
(the `api-lint` CI job runs the same). Keep `.claude/rules/rest-api.md` and the
README in step.

## 5. Behaviour

Add the happy path and each refusal to `features/*.feature` (`features/steps_*_test.go`
drive the use cases) and a case to `internal/adapters/inbound/http/http_test.go`. The
real wiring on Postgres and Kafka is proven by `cmd/api/e2e_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`, `go test -tags=integration ./cmd/... -race -count=1`,
and the spectral lint above.
