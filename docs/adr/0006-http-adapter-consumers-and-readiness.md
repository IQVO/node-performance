# ADR 0006: the HTTP adapter, the consumers' failure policy, readiness and shutdown

## Status

Accepted (2026-10-23).

## Context

`apis/openapi.yaml` fixes two read operations; the model conventions fix the shared problem
types. The fleet requires a dependency-aware `/readyz`, a NUL guard and a graceful
shutdown. ADR 0003 fixed what the consumers do with a message; this ADR fixes what they do
when it cannot be applied, and how the whole process starts and stops.

## Decision

1. **Router.** `internal/adapters/inbound/http` is a chi router over the query use cases
   (`Server` holds one field per operation; `NewRouter` refuses a server missing one).
   Middleware, outermost first: access log, recoverer (a panic is a 500 `internal-error`
   the log still sees), `rejectNUL`, then the route. The adapter imports only the
   application layer; `cmd/api` injects the use cases and the readiness gate and never
   imports the memory or repocontract packages.
2. **Operations.** `GET /scorecards/{nodeId}` (both windows, 7d then 30d) and `GET
   /scorecards?grade=&window=&limit=&after=`, plus the probes `GET /healthz` and `GET
   /readyz`. An empty list is `{"items":[]}`, never null. There is no state-changing route
   and so no Idempotency-Key middleware (ADR 0002, A21).
3. **Problems (RFC 9457, `application/problem+json`).** `type` is the slug, `title`,
   `status`, `detail` (the error text; an unmapped error is an opaque 500 whose cause is
   logged) and `instance` (the path). `not-found` 404, `validation-failed` 422,
   `concurrent-update` 409, `malformed-request-query` 400 (unknown parameter, a `limit`
   that is not an integer, NUL), `malformed-request-body` 400 (NUL in a body),
   `method-not-allowed` 405, `dependency-unavailable` 503. A `limit` below 1 or above 500
   is a 422, not a silent default.
4. **NUL.** A NUL (U+0000) cannot be stored in Postgres text and would abort the
   transaction: 404 in the path, 400 in the query and the body (raw byte or the `\u0000`
   escape).
5. **Consumers.** One consumer per consumed topic, one stable group each. A message is
   handled by exactly one intake use case; the offset is committed afterwards. The failure
   policy: not a CloudEvents 1.0 message or an unhandled type is skipped; an undecodable or
   invalid payload (`ErrInvalidEvent`) is dead-lettered to `<topic>.dlq` at once; any other
   error is transient and the SAME message is retried up to 5 attempts with a 200 ms
   backoff doubling to 5 s, then dead-lettered. The dead-letter publish is retried until it
   succeeds, and a shutdown never dead-letters a message in flight
   (`TestDeadLetterCallsGuardCancellation`). An acknowledgement or handover whose purchase
   order facts have not arrived yet is transient on purpose: the producers' topics race.
6. **Scheduler.** `cmd/api` runs `Recompute` at boot and every `RECOMPUTE_INTERVAL`
   (default 1 m); a failed round is logged and retried at the next tick.
7. **Probes.** `/healthz` is liveness, never flipped. `/readyz` is 200 only while the drain
   gate is open AND Postgres answers a ping AND a Kafka broker answers a metadata request
   naming NO topics (an empty, non-nil list: it never creates a topic and does not require
   the output topic to exist), on a fresh connection per check (a cached layout would keep
   answering ready after the broker is gone). Verdicts are cached 3 s and bounded 2 s.
   While draining: 503 `{"status":"not_ready"}`; while a dependency fails: 503
   `dependency-unavailable` naming it.
8. **Composition root.** `cmd/api` `run(ctx)` validates the environment before anything
   opens (`DATABASE_URL`, `KAFKA_BROKERS`, the five `*_CONSUMER_GROUP`: a boot error names
   the variable), runs the embedded migrations and pings Postgres with backoff
   (`internal/bootretry`), then starts the workers and serves. Shutdown order: `/readyz`
   flips to 503, wait `SHUTDOWN_DRAIN_DELAY` (default 5 s), drain HTTP (10 s), stop and
   await the consumers and the scheduler, stop and await the relay LAST, close the pool. If
   a worker returns on its own the process exits non-zero.

## Consequences

- A poison message never wedges a partition and is never lost: it is on the dead-letter
  topic with the reason in its headers.
- Because the producers' topics are separate, an event can wait on the dead-letter topic
  only after five attempts; replaying it from there after the facts arrived is safe (the
  inbox deduplicates).
- The shutdown bounds add up to 35 s: the chart's grace period must exceed it.
