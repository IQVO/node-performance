# ADR 0008: a web console remote

## Status

Accepted (2026-10-24).

## Context

The fleet's operator surface is the warehouse-console shell, a Module
Federation host that lazy-loads one remote per bounded context. Every context
with reads ships a remote under `web/` (task-interleaving first, then the
others); node-performance had none, so its scorecards were reachable only
through the REST API or the MCP server of ADR 0007. The service has exactly
two reads, both already JSON, so the remote is a thin read-only UI.

## Decision

- **`web/` is a Module Federation remote, `node_performance_mfe`.** Vite +
  `@module-federation/vite`, exposing exactly `./App` (default export, no
  props). The console mounts it under its own `/node-performance/*` splat
  route; all routes inside are relative, so the same build runs standalone at
  `/` for local development (`npm run dev`, port 4016 — the fleet's unique
  port ladder had every 5181-5197/4010/4011 slot taken). The Vite config
  stays a plain object (never the callback form: a callback cannot be
  `mergeConfig`-merged, which has killed test suites elsewhere in the fleet).
- **Two read-only screens.** "Scorecards by grade" (GET `/scorecards`, the
  grade filter required, the window filter optional, keyset paging on
  `after`) and "Node lookup" → "Node N-…" (GET `/scorecards/{nodeId}`, both
  windows with their metrics). Nothing else: no write call exists in
  `api.ts`, and every request is a header-less GET. Types mirror the OpenAPI
  schemas; errors render the RFC 9457 problem (`not-found`,
  `validation-failed`).
- **The API location is runtime, not build time.** `src/config.ts` resolves
  `${window.__WAREHOUSE_CONFIG__.apiOrigin}/api/node-performance` — the
  console publishes `/config.json` before any remote mounts. A production
  build without an origin throws rather than falling back to a developer
  port; only `npm run dev` uses `http://localhost:8080`.
- **Its own image, its own workload.** `web/Dockerfile` is the fleet's
  two-stage build: `@warehouse/ui-kit` is a sibling checkout
  (`file:../../warehouse-ui-kit`) supplied as a named BuildKit context
  (`--build-context uikit=../../warehouse-ui-kit`), the remote builds on
  `node:22-bookworm-slim`, and the runtime is `nginx-unprivileged` serving
  the static bundle with `web/nginx.conf` (no API is proxied; `/healthz`
  serves the probes). The chart (behind `frontend.enabled`, off by default)
  deploys it as its own ClusterIP Deployment/Service — never routed by the
  Gateway API HTTPRoute; the Nginx web gateway serves it at
  `/mfes/node-performance/`, which is also the build's asset base so the
  remote's chunks stay in their own namespace at the shared origin.
- **Gates.** `web/` is npm-only: oxlint, `tsc -b`, vitest (+@testing-library,
  jsdom) and `vite build` run locally and in the CI `web` job, which checks
  warehouse-ui-kit out as a sibling the same way the console's CI does.

## Consequences

- Operators read a node's grades and the grade distribution without curl or
  an agent; the remote adds no API and no permission surface of its own.
- The pinned host/remote contract (container name, `./App`, asset base,
  port 4016) changes only together with warehouse-console's matching entry.
- The ui-kit dependency stays a sibling checkout, so the CI job (and every
  contributor) needs the two repositories next to each other — the same
  shape every other remote in the fleet already has.
