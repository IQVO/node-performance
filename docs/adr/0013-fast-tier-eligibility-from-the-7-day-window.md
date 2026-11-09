# ADR 0013: fast-tier eligibility from the 7-day window

## Status

Accepted (2026-11-09).

## Context

Until now the 7-day window fed alerts only (G20, E18): consumers decide on the 30-day grade and the 7d scorecard only
raises `NodeThresholdBreached` on the analytics topic. A node whose recent on-time handover collapses therefore keeps
winning SAME_DAY and NEXT_DAY sourcing until its 30-day grade drops, which can take weeks. Public marketplaces revoke
fast-delivery badges on short measurement windows for the same reason. The 7d scorecard is the short window this
context already computes, so the fast-tier decision belongs here, next to the facts it is made from, and is published
as an integration event that delivery-promise can fold into a local read model (its own ADR).

## Decision

### Policy `FastTierEligibility`

A policy of the NodeScorecard domain (`internal/domain/scorecard`), evaluated by `Recompute` on the **7d** scorecard
only. The 30d scorecard never evaluates it.

- Tiers governed: `SAME_DAY` and `NEXT_DAY`. Both follow one eligibility state per node (one flag), published as one
  event per tier.
- Every node starts ELIGIBLE. A PROVISIONAL scorecard (below `MinObservations`) never changes eligibility.
- **Suspension.** The 7d scorecard is GRADED and `onTimeHandoverRate < 0.90` (`suspendBelow`): the node becomes
  ineligible.
- **Reinstatement (automatic).** An ineligible node whose 7d scorecard is GRADED and `onTimeHandoverRate >= 0.95`
  (`reinstateAt`) on `reinstateAfterDays = 3` consecutive distinct UTC calendar days of recompute becomes eligible. A
  recompute below 0.95 resets the streak. The 0.90/0.95 gap is the hysteresis. The three numbers are constants in
  the domain, in the style of `gradingBars`.
- **Persistence.** The flag, the streak count and the streak's last UTC day live on the 7d NodeScorecard aggregate:
  Snapshot struct, `New`/`Open`, `Restore`, the `Snapshot()` builder, the repocontract aggregates assertion and a
  migration (0004).

Reading of the rules the code follows (so the tests pin them):

- A recompute whose UTC day equals the streak's last day changes nothing (the same day twice counts once).
- A GRADED recompute at or above 0.95 on a later day adds one to the streak; the third such day reinstates and clears
  the streak. "Consecutive" counts successive days on which a recompute ran: a recompute is only due when
  observations arrived or left the window, so a quiet calendar day is not a miss. Only a GRADED recompute below 0.95
  resets the streak. A PROVISIONAL recompute neither advances nor resets it.
- A rate in [0.90, 0.95) leaves an eligible node eligible (no suspension) and resets the streak of an ineligible one.

### Integration event `NodeTierEligibilityChanged`

Topic `retailnetwork.node-performance.events` (NOT analytics).

- type `com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged`; source
  `/retailnetwork/node-performance`; dataschema `urn:retailnetwork:node-performance:events:NodeTierEligibilityChanged:v1`;
  subject and Kafka key = the scorecardId (`<nodeId>:7d`).
- data: `nodeId` (string), `tier` (`SAME_DAY` | `NEXT_DAY`), `eligible` (bool), `metric` (`onTimeHandoverRate`),
  `value` (number, the rate that caused the change), `threshold` (0.90 on suspension, 0.95 on reinstatement),
  `window` (`7d`), `changedAt` (RFC 3339 UTC).
- Raised only when the flag FLIPS; two events per flip (one per tier), same `changedAt`, in the same transaction as the
  scorecard save (outbox). No event for the initial ELIGIBLE state.
- The full type string appears in the type catalogue (ADR 0004: table row and `type: ... stream: events` line) and in
  `apis/asyncapi.yaml` (published section).

### Observability

- Gauge `nodeperf_fast_tier_ineligible_nodes`: the number of nodes currently suspended. The instrument is the OTel
  observable gauge `nodeperf.fast_tier.ineligible_nodes` (unit `{node}`), which the Prometheus exporter renders as
  exactly that name; it reads the count from the database at each collection, so it survives a restart and a leader
  change of the recompute scheduler.
- An INFO log line `fast tier eligibility changed` on each flip, with `node_id`, `eligible`, `value`, `threshold`.

### Scope

No REST change. No MCP change. No A2A change. Docs: the event catalogue page, the integration page, the ubiquitous
language and the ADR index are updated; the "7d feeds alerts only" text (G20) now says that it also gates the fast tiers.

## Consequences

- A collapse of on-time handover is visible to delivery-promise within one recompute interval of the observations
  that caused it, instead of weeks later.
- The hysteresis (0.90 down, 0.95 up, three days) stops a node at the boundary from flapping; the price is that
  reinstatement takes at least three recompute days even for a node that recovered at once.
- A suspended node that stops reporting is only recomputed when observations leave its window, so its streak advances
  only on days a recompute runs. A node that goes silent below 0.90 stays suspended until fresh facts say otherwise.
- One more column set on `scorecards` (migration 0004); the 30d rows keep the defaults and ignore them.
- Consumers must treat a missing event as ELIGIBLE (every node starts eligible) and may see a flip more than once
  (at-least-once delivery); the state they hold is the `eligible` of the latest event per `nodeId` and `tier`.
