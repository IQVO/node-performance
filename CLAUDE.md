# node-performance

harness-template: v4

Retail network, bounded context Node Performance (Supporting, partner tier). Owns the NodeScorecard (a 7-day and a 30-day one per node), grades every node from observed facts only, publishes NodeScorecardUpdated and NodeThresholdBreached, consumes thirteen event types from five producers and calls no sibling.
Design source of truth: IQVO/retail-network-docs `model/model.yaml` (context node-performance). This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD; the pre-push gate), `make coverage` (>= 90% over internal/domain + internal/application), `make mutation` (gremlins per internal/domain/<pkg>, thresholds 95/95, below the measured 100),
`make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln`. Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean. Never gate a commit on a piped command; run the integration suite as a background job writing to a file.

## Layout (hexagonal)
`internal/domain/{shared,scorecard}` pure Go; `internal/application/{ports (interfaces only),repository (filters),views,usecases,outbox}`;
`internal/adapters/inbound/{http (chi),kafka (5 consumers)}`; `internal/adapters/outbound/{postgres,kafka,outbox,clock,memory,repocontract}`; `internal/adapters/kafka/cloudevents` (the ONLY envelope builder);
`internal/testing/{pgtest,kafkatest}` (testcontainers helpers); `internal/bootretry`; `cmd/api` (composition root, `run(ctx)`); `features/` (godog, never a .go file at the repo root). Migrations are embedded in `internal/adapters/outbound/postgres/migrations`.

## Runtime rule
Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS` and the five `*_CONSUMER_GROUP` variables are mandatory (boot error naming the variable, raised before anything opens; `MIGRATIONS_DATABASE_URL` is an optional direct DSN, falls back to `DATABASE_URL`).
Workers: five consumers, the recompute scheduler (`RECOMPUTE_INTERVAL`, 1m) and the always-on outbox relay (`OUTBOX_MAX_ATTEMPTS` default 20, 0 = unlimited; `OUTBOX_RELAY_INTERVAL` 1s); the process exits non-zero when any of them ends on its own.
`/healthz` = liveness; `/readyz` = drain gate + Postgres ping + Kafka metadata request with an EMPTY non-nil topic list (fresh connection per check; it never needs the topic to exist).
Shutdown: /readyz 503, `SHUTDOWN_DRAIN_DELAY` (5s), drain HTTP, stop consumers and scheduler, stop relay LAST, close pool. cmd/** never imports internal/adapters/outbound/memory or repocontract. No authentication on REST. See docs/runtime.md.

## REST
chi router implementing both operations of apis/openapi.yaml (GET /scorecards/{nodeId}, GET /scorecards?grade=) plus /healthz, /readyz. Reads only: no Idempotency-Key (ADR 0002 A21). NUL: 404 in the path, 400 `malformed-request-query|body` elsewhere.
Errors are RFC 9457 problem+json, `type` = slug: not-found, validation-failed, concurrent-update, malformed-request-query/body, method-not-allowed, dependency-unavailable (ADR 0006).

## Events: CloudEvents 1.0 is MANDATORY
Published: type `com.retailnetwork.partner.node-performance.scorecard.<Event>`; source `/retailnetwork/node-performance`; dataschema `urn:retailnetwork:node-performance:<events|analytics>:<Event>:v1`;
NodeScorecardUpdated ONLY on `retailnetwork.node-performance.events`, NodeThresholdBreached ONLY on `retailnetwork.node-performance.analytics`; subject and Kafka key = scorecardId (`nodeId:window`); header `content-type: application/cloudevents+json; charset=UTF-8`.
Use internal/adapters/kafka/cloudevents only. Transactional outbox: scorecards + outbox rows in ONE Postgres transaction, id minted once at append, at-least-once delivery. No `warehouse.*` topic, ever.
Consumed: vendor-connectivity, shipping-and-tracking, delivery-promise, offer-availability, node-topology topics; dispatch on the FULL type, tolerant decoding, inbox `processed_events` keyed by the CloudEvents id, DLQ `<topic>.dlq` (ADR 0003, 0006).
A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit + encoder payload + golden test (.claude/skills/how-to-add-an-integration-event).

## Domain
Aggregate, metric definitions and assumptions A1-A23 are in docs/adr/0002; consumed contracts and PurchaseOrderFacts in 0003; outbox/relay/cap in 0005; HTTP, consumers, readiness in 0006. MinObservations = 50 (below it a scorecard is PROVISIONAL and publishes nothing). Timestamps are UTC at microsecond precision (shared.Instant).

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Deploy: charts/node-performance (api only; DSN from a Secret, five `config.*ConsumerGroup` values and `kafka.brokers` required, grace period >= drain + 35 s); tests `bash charts/node-performance/tests/render_test.sh` and `python3 charts/node-performance/tests/test_service_selectors.py`. Open follow-up: repository secrets (docs/before-first-release.md row 11).
