# ADR 0005: the outbox relay, its attempt cap and the CloudEvents encoder

## Status

Accepted (2026-10-23).

## Context

ADR 0003 decided that every integration event is published through the transactional
outbox: the recompute use case saves the scorecards and appends the encoded events in ONE
Postgres transaction. Something has to drain the rows to Kafka. The harness template
(`TestOutboxRelaysPassAttemptCap`) forbids a relay that one poison row can block forever.

## Decision

1. **Relay.** `internal/adapters/outbound/outbox` holds `NewOutboxRelay(store, sink, logger,
   opts...)`. A pass (`RelayOnce`) asks the store to claim up to a batch of unpublished
   rows in id order, sends them one at a time and marks each row published only after the
   send returned nil. `Run` repeats until its context is cancelled: a full batch is
   followed at once by another pass, anything else sleeps `OUTBOX_RELAY_INTERVAL`
   (default 1 s); a failed pass is logged and retried after the interval. The relay never
   dials: a broker that is down at boot only makes passes fail.
2. **Store.** `postgres.RelayStore.Drain` claims with `FOR UPDATE SKIP LOCKED` (two
   replicas never claim the same row), stops at the first send failure (a later event of a
   scorecard never overtakes an earlier one that is being retried), records `attempts` and
   `last_error` on the failed row, commits the rows already sent and returns an
   `*outbox.SendFailure`. A cancelled context still commits what was sent. The write side
   (`postgres.OutboxRepo.Append`) encodes before it writes, so an event the encoder
   refuses rolls the whole unit of work back.
3. **Delivery is at-least-once.** A crash between the send and the `UPDATE` republishes
   that row with the SAME CloudEvents `id` (minted once, at append, and stored in
   `outbox_events.event_id`), so consumers deduplicate on the id.
4. **Attempt cap.** `WithMaxAttempts(n)` (env `OUTBOX_MAX_ATTEMPTS`, default 20, `0` =
   unlimited, a negative or unparsable value falls back to 20 with a warning) excludes a
   row from future claims once it failed `n` times. The row stays in `outbox_events` with
   `attempts` and `last_error`; the failure that reaches the cap is logged once at ERROR
   with the row id, topic, type and key; rows behind it, including rows with the same key,
   overtake it. An operator re-arms it with `UPDATE outbox_events SET attempts = 0 WHERE id
   = ...`. **Trade-off:** after the cap a later event of the SAME scorecard can reach the
   topic before the capped one, so consumers must not rely on per-key order across a capped
   poison row. `cmd/api` passes `WithMaxAttempts(...)` inline.
5. **Sink.** `kafka.RelaySink` writes each message to the topic it names with one
   `kafka.Writer` that has NO fixed topic, `RequireAll` acks, the `Hash` balancer on the
   key, a 10 ms batch timeout (the default 1 s would cap a relay that sends one row per
   call at one event per second) and `AllowAutoTopicCreation`.
6. **First write to a new topic.** The broker creates a topic on its first write, and that
   first write can fail with `UnknownTopicOrPartition` or `LeaderNotAvailable`. This is an
   ordinary send failure: the row keeps its place, the next pass retries it and publishes
   the same id. `/readyz` therefore never asks for topics (ADR 0006).
7. **Encoder.** `kafka.Encoder` implements `outbox.Encoder` for the two events and builds
   every envelope through `internal/adapters/kafka/cloudevents`: `type`
   `com.retailnetwork.partner.node-performance.scorecard.<Event>`, `source`
   `/retailnetwork/node-performance`, `subject` and Kafka key = the scorecardId,
   `dataschema` `urn:retailnetwork:node-performance:<events|analytics>:<Event>:v1`, Kafka
   header `content-type: application/cloudevents+json; charset=UTF-8`.
   `NodeScorecardUpdated` goes ONLY to `retailnetwork.node-performance.events` and
   `NodeThresholdBreached` ONLY to `retailnetwork.node-performance.analytics` (selected by
   `AnalyticsOnly()`). The payloads are the ones of `apis/asyncapi.yaml`, pinned by golden
   exact-JSON tests (`internal/adapters/outbound/kafka/testdata`).

## Consequences

- A poison row costs at most `OUTBOX_MAX_ATTEMPTS` passes of delay for the rows behind it,
  never a permanent block.
- Postgres integration tests prove Drain (order, failure recording, the cap and re-arming,
  concurrent drains); a Kafka integration test proves that the outbox rows become exactly
  the CloudEvents on the right topics.
