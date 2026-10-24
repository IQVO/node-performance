# Architecture decision records

The decisions of `node-performance`, in the order they were taken. The ADR bodies
are immutable; a changed decision gets a new ADR that supersedes the old one.
The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-node-performance-bounded-context.md) | Node Performance is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | the NodeScorecard aggregate, its invariants and the assumptions the model left open | Accepted (2026-10-23) |
| [0003](./0003-consumed-contracts-and-local-facts.md) | consumed contracts, local facts, relationships and recompute | Accepted (2026-10-23) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-23) |
| [0005](./0005-outbox-relay-and-attempt-cap.md) | the outbox relay, its attempt cap and the CloudEvents encoder | Accepted (2026-10-23) |
| [0006](./0006-http-adapter-consumers-and-readiness.md) | the HTTP adapter, the consumers' failure policy, readiness and shutdown | Accepted (2026-10-23) |
| [0007](./0007-mcp-server-adoption.md) | a read-only MCP server | Accepted (2026-10-24) |
| [0008](./0008-console-remote.md) | a web console remote | Accepted (2026-10-24) |
| [0009](./0009-inbound-timeliness-and-accuracy-metrics.md) | inbound timeliness and accuracy, two informational scorecard metrics | Accepted (2026-10-24) |
