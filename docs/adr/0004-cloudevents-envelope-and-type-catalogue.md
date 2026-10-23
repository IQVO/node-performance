# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-23).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (retail-network-docs `model/model.yaml` `conventions.cloudevents`; the repo
rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every
type node-performance publishes. `TestEventCatalogueMatchesContract` checks it
against `apis/asyncapi.yaml`. The types node-performance only consumes are listed
in ADR 0003, not here.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source`
  `/retailnetwork/node-performance`; `subject` the aggregate root id, the
  `scorecardId` (`nodeId:window`, for example `node-0f3a91:30d`); `time` the
  instant of the recompute, in UTC; `datacontenttype` `application/json`;
  `dataschema` `urn:retailnetwork:node-performance:<stream>:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one scorecard stay ordered on one partition.
- `data` is camelCase; optional fields are omitted when absent; timestamps are
  RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase, no separators: `scorecard`
  (both events are raised by NodeScorecard). Tier is `partner`.

### Published types

`NodeScorecardUpdated` is an integration event and goes ONLY to
`retailnetwork.node-performance.events`. `NodeThresholdBreached` has no
in-organization consumer ("analytics only" in the model) and goes ONLY to
`retailnetwork.node-performance.analytics` (conventions.analytics_rule; contracts-delta
shared conventions), under the dataschema stream `analytics`.

```text
type: com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated stream: events
type: com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached stream: analytics
```

| Event | Subject / key | Raised when | `data` |
|---|---|---|---|
| `NodeScorecardUpdated` | `scorecardId` | a Recompute is saved and the state, grade or metrics changed, except while the scorecard stays PROVISIONAL | `{nodeId, window, grade, onTimeHandoverRate, lateAckRate, rejectRate, expiryRate, promiseKeptRate, feedFreshnessP95Minutes, observations, computedAt}` |
| `NodeThresholdBreached` | `scorecardId` | a Recompute between two GRADED states is saved in which a metric moved to the unhealthy side of its alert threshold (one event per metric) | `{nodeId, metric, value, threshold, window}` |

Payload rules:

- The `data` field lists are exactly the model's `publishes[].data` lists; the
  wire types are recorded in `apis/asyncapi.yaml` (ADR 0002, A12).
- `window` is `7d` or `30d`; `grade` is `A`, `B`, `C`, `D` or `PROVISIONAL`
  (adds no handicap, G20); consumers use the 30d window, the 7d window feeds
  alerts only (E18).
- `feedFreshnessP95Minutes` and `observations` are integers; the five rates are
  numbers in [0,1].
- A breaking payload change is a new `.v2` type and dataschema, never a
  mutation of v1.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- Adding a published type requires a line here in the same commit as its
  asyncapi message, or the architecture fitness test fails.
