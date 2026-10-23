---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event in this service: CloudEvents 1.0 type naming, AsyncAPI, the type catalogue ADR, the encoder, the transactional outbox, the inbound consumers. Use when touching internal/adapters kafka or outbox code, a domain event, or apis/asyncapi*.yaml.
---

# How to add an integration event

node-performance publishes two events and consumes thirteen event types (ADR 0003,
ADR 0004). The envelope is CloudEvents 1.0 only; read
`.claude/rules/integration-events.md` and `.claude/rules/fleet/cloudevents.md`
first. The worked example is `NodeThresholdBreached` (published, analytics only) and
`PromiseKept` (consumed).

## Publishing a new event

1. **Domain event.** A typed struct in `internal/domain/scorecard/events.go`
   (`NodeThresholdBreached`): embeds `shared.Header`, fields exactly the model's
   `data` list, methods `EventName()`, `Entity()` (`scorecard`), `SubjectID()` (the
   scorecardId) and `AnalyticsOnly()`, plus a `var _ shared.Event = ...` assertion.
   `NodeScorecard.Recompute` returns it; `Version()` after the command is the version
   it carries.
2. **Contract, one commit.** Add the message and its `data` schema to
   `apis/asyncapi.yaml`, AND one
   `type: com.retailnetwork.partner.node-performance.scorecard.<Event> stream: events|analytics`
   line to `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.
   `TestEventCatalogueMatchesContract` fails when the two disagree, so the push fails
   when they are not in the same commit. Lint:
   `npx --yes @stoplight/spectral-cli lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml`.
   Evolution is additive only; a breaking change is a new `:v2` dataschema.
3. **Encoder.** In `internal/adapters/outbound/kafka/encoder.go` add the payload
   struct (camelCase json tags) and a `case` in `payloadFor`; the stream and topic come
   from `AnalyticsOnly()`. The envelope itself is built by
   `internal/adapters/kafka/cloudevents` only; never hand-build one. Add a golden file
   under `internal/adapters/outbound/kafka/testdata/` and a row to
   `TestGoldenEnvelopes` in `encoder_test.go` (an exact-JSON test of every attribute
   and the payload; regenerate with `-update`, then read the diff).
4. **Publish from the use case**: the aggregate returns the events and
   `writer` appends them in the same `ports.UnitOfWork` as the `Save`
   (`internal/application/usecases/writer.go`), so the aggregate row and the outbox
   row commit or roll back together. Never publish after the commit and never call
   Kafka from a use case.
5. **Nothing else.** The relay (`internal/adapters/outbound/outbox/relay.go`) already
   drains every row to the topic the message names.
6. **Prove it.** `go test -tags=integration ./internal/adapters/outbound/kafka/ -race -count=1`
   reads the events back from a real broker; extend `cmd/api/e2e_integration_test.go`
   when the event is part of a flow.

## Consuming a new event from a sibling context

1. Read the producer's `apis/asyncapi.yaml` (`git show origin/<branch>:apis/asyncapi.yaml`);
   add the fields you rely on, and only those, to the `receive` side of
   `apis/asyncapi.yaml` and to the table of ADR 0003 (an ADR amendment).
2. Add the payload struct and the handler to
   `internal/adapters/inbound/kafka/handlers.go` (`handle[T]` decodes tolerantly and
   wraps a bad payload as `errBadPayload`, which is dead-lettered at once) and the full
   `type` constant; dispatch is on the FULL type, never a suffix.
3. Add the intake method in `internal/application/usecases/intake.go`: it runs in ONE
   unit of work with the inbox claim (`processed_events`, keyed by the CloudEvents id),
   so a redelivery is a no-op; a value the domain refuses becomes `ErrInvalidEvent`.
4. A new topic needs its own consumer group env var (a boot error naming it, listed
   in `docs/runtime.md` and the chart) and a `Consumer` in `NewConsumers`.
5. Tests: the handler in `consumer_test.go` over the memory doubles, the loop in
   `loop_test.go`, and the real broker in `consumer_integration_test.go`.

## Verify before opening the PR

`make check`, `go test ./internal/architecture/ -count=1` (catalogue, CloudEvents-only,
relay cap, DLQ shutdown), the spectral lint of the AsyncAPI, and the integration command
above.
