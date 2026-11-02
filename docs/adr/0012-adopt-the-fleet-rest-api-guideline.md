# ADR 0012: adopt the fleet REST API guideline

## Status

Accepted (2026-11-01).

## Context

The fleet REST API guideline (warehouse-docs, rules R1 to R8) is mandatory for every `apis/openapi.yaml`: URLs name
nouns, every POST and PATCH is idempotent (`Idempotency-Key`, `409 idempotency-key-reused`, `Idempotency-Replayed`),
and every collection is paged with `limit` + `cursor` and answers `{items, nextCursor}`.

## Decision

- **Ruleset.** `.spectral.yaml` carries the `rest-guideline` rule (`.spectral/functions/restGuideline.js`); the
  `api-lint` job runs it with `--fail-severity=warn`.
- **Routes.** No rename: both operations are already nouns (`GET /scorecards/{nodeId}`, `GET /scorecards`).
- **Idempotency.** The service is read-only (ADR 0002, A21): there is no POST, PATCH, PUT or DELETE, so there is no
  middleware, no key table and no `409 idempotency-key-reused`. The guideline's key rules apply the day a write route
  exists; it then needs the middleware, joined to the unit of work.
- **Pagination.** `GET /scorecards` takes `grade`, `window`, `limit` (1 to 500, default 100) and the opaque `cursor`
  (base64url JSON `{s,i}` from `internal/adapters/inbound/http/page`, copied from the fleet reference). The keyset is
  the `scorecardId`, which is unique, so the sort key and the id are the same value; the repositories and the use case
  are unchanged and the HTTP adapter wraps and unwraps the cursor. `after` is retired.
- **Problems.** `400 invalid-limit` (absent is 100; empty, repeated, non-integer or out of 1..500 is refused),
  `400 invalid-cursor`, `400 unknown-query-parameter` (any parameter other than `grade`, `window`, `limit`, `cursor`,
  e.g. a retired `after`, `offset`, `nextToken`). `422 validation-failed` stays for a missing or unknown `grade` and
  an unknown `window`. `malformed-request-query` remains for a NUL character.
- **Exemptions.** None: `GET /scorecards/{nodeId}` is an item, not a collection.
- **MCP and A2A** keep their own tool schemas (`after` is the scorecardId there); they call the same use case.

## Consequences

Breaking for the one paged consumer: the console remote (`web/src/api.ts`, in this PR) sends `cursor` instead of
`after`. Deploy with the REST cutover set.
