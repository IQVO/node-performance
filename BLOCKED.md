# BLOCKED: savepoint fix not applicable to node-performance

The task asks for a SAVEPOINT around the handler call in the transactional Idempotency-Key middleware.
This repository has no such middleware, so there is nothing to patch.

Evidence (checked on feature/savepoint-fix, cut from origin/develop):
- `git grep -l 'Idempotency-Key' -- '*.go'` matches only `internal/adapters/inbound/http/nul.go` and `problems.go`, both comments.
- `internal/adapters/inbound/http/problems.go` package doc: "There is no state-changing route, so there is no Idempotency-Key middleware (ADR 0002, A21)."
- The two REST operations are reads (GET /scorecards/{nodeId}, GET /scorecards?grade=).
- The `idempotency_keys` table exists (migration 0002) but no code uses it (docs/adr/0002 line 196, docs/docs/overview/runtime.md).
- Commit 45e3714 is not present in this clone, so the reference diff could not be read locally.

The invariant ("a mapped 4xx over an aborted tx is still recorded; a 2xx over an aborted tx is never returned or cached") has no code path here to guarantee.
Writing a new middleware would add route/middleware behavior beyond the fix, which the task forbids. It would also contradict ADR 0002 A21.

Related, already handled: `internal/adapters/outbound/postgres/unit_of_work.go` wraps unit work in a SAVEPOINT, so a failed statement does not poison the unit's transaction (25P02).

Decision needed from a human: close this task as not applicable, or open an ADR first if a state-changing route is planned.
