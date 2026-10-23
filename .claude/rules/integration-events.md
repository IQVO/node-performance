---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

node-performance PUBLISHES two events (`NodeScorecardUpdated` to
`retailnetwork.node-performance.events`, `NodeThresholdBreached` to
`retailnetwork.node-performance.analytics`) and CONSUMES thirteen event types from
five producers (ADR 0003). Never produce or consume a `warehouse.*` topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces is a CloudEvents 1.0 event in structured
content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no envelope
  toggle env var. `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event`
  through ONE helper package, `internal/adapters/kafka/cloudevents` (`New`, `Decode`,
  `ContentTypeHeader`). Transport stays `segmentio/kafka-go`. The only producer of
  envelopes is the encoder `internal/adapters/outbound/kafka/encoder.go`.
- Every message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`. Message key = the
  `scorecardId`, `kafkago.Hash{}` balancer (`internal/adapters/outbound/kafka/relay_sink.go`).
- Attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per event at outbox append
  and stored in `outbox_events.event_id`, so a redelivery carries the same id);
  `source=/retailnetwork/node-performance`; `type`; `subject` (the scorecardId, never
  empty); `time` (the domain occurred-at, UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:node-performance:<events|analytics>:<EventName>:v1`.
  No custom extension attributes without an ADR.
- `type` = `com.retailnetwork.partner.node-performance.scorecard.<EventName>`. A
  breaking payload change is a new `:v2` dataschema, never a mutation of v1; evolution
  is additive only.
- Payloads follow the wire conventions of contracts-delta: ids are strings, instants
  RFC 3339 UTC, durations integers ending in `Minutes`, enums UPPER_SNAKE, optional
  fields omitted, arrays always present.
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/encoder_test.go`, files in `testdata/`), the
  catalogue-vs-contract sensor (`TestEventCatalogueMatchesContract`), and the Kafka
  proof against a real broker (`internal/adapters/outbound/kafka/relay_integration_test.go`),
  all via testcontainers.

### Published types

| `type` | stream / topic | `subject` |
| --- | --- | --- |
| `com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated` | events / `retailnetwork.node-performance.events` | scorecardId |
| `com.retailnetwork.partner.node-performance.scorecard.NodeThresholdBreached` | analytics / `retailnetwork.node-performance.analytics` (analytics only: never on the events topic) | scorecardId |

### Consumed types (dispatch on the FULL `type`; handlers in `internal/adapters/inbound/kafka/handlers.go`)

| Topic | `type` | Effect |
| --- | --- | --- |
| `retailnetwork.vendor-connectivity.events` | `...vendor-connectivity.purchaseorder.PurchaseOrderIssued` / `VendorShipmentConfirmed` | upsert `PurchaseOrderFacts` |
| | `...purchaseorder.PurchaseOrderAccepted` / `PurchaseOrderRejected` / `PurchaseOrderExpired` | observation `ACK_ACCEPTED` / `ACK_REJECTED` / `ACK_EXPIRED` |
| | `...vendoraccount.VendorOnboarded`, `...submission.SubmissionProcessed` | none (inbox row only) |
| `retailnetwork.shipping-and-tracking.events` | `...journey.PackageHandedOver` | observation `HANDOVER` |
| `retailnetwork.delivery-promise.events` | `...promise.PromiseKept` / `PromiseBroken` | observation `PROMISE_KEPT` / `PROMISE_BROKEN` (node-attributable causes only) |
| `retailnetwork.offer-availability.events` | `...inventoryfeed.InventoryFeedApplied` | observation `FEED_APPLIED` |
| `retailnetwork.node-topology.events` | `...node.NodeRegistered` / `NodeOperationalProfileDeclared` | none (inbox row only) |

Decoding is tolerant: only the fields of ADR 0003 are required, unknown fields and
unknown types are ignored. The vendored contract is the `receive` side of
`apis/asyncapi.yaml`.

## Transactional outbox

A use case saves the aggregates and appends the encoded events in ONE Postgres
transaction (`ports.EventOutbox`, `internal/adapters/outbound/postgres/outbox_repository.go`).
The relay (`internal/adapters/outbound/outbox/relay.go`, always on, started by
`cmd/api/workers.go`) drains `outbox_events` in id order, at-least-once
(`postgres.RelayStore.Drain`). The cap `OUTBOX_MAX_ATTEMPTS` (default 20, 0 =
unlimited) excludes a poison row; re-arm it with
`UPDATE outbox_events SET attempts = 0 WHERE id = ...` (ADR 0005). The first write to
a topic that does not exist yet can fail once with an unknown-topic error: the relay
retries it with the same id. The caller of `NewOutboxRelay` must pass
`WithMaxAttempts(...)` (`TestOutboxRelaysPassAttemptCap`).

## Consumers

Five consumers, one per consumed topic, each under its own group from a mandatory env
var (`VENDOR_CONNECTIVITY_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP`,
`DELIVERY_PROMISE_CONSUMER_GROUP`, `OFFER_AVAILABILITY_CONSUMER_GROUP`,
`NODE_TOPOLOGY_CONSUMER_GROUP`; a boot error names the missing one, never a string
literal: `TestKafkaConsumerGroupNeverHardcodedInline`). Offsets are committed after the
use case returned; the inbox (`processed_events`, keyed by the CloudEvents id) makes a
redelivery a no-op; a transient failure is attempted up to 5 times with capped backoff and then
dead-lettered to `<topic>.dlq`, a payload that can never be applied goes there at once
(`TestDeadLetterCallsGuardCancellation` keeps a shutdown from dead-lettering).
Rules: `.claude/rules/fleet/kafka-testing-and-consumers.md`.
