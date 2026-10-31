---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
description: "The Node Performance test pyramid: unit, integration, godog, contract, mutation and fitness tests, the make targets and the CI jobs."
---

# Testing

All Go code is tested at five levels; the make targets mirror the CI jobs
(`.github/workflows/ci.yml`) one to one.

| Level | What | Where | Command | Needs |
| --- | --- | --- | --- | --- |
| Unit and handler | domain invariants, use cases over in-memory test doubles, `httptest` handlers, consumer loops, config parsing | `internal/domain/**`, `internal/application/**`, `internal/adapters/**`, `cmd/*/*_test.go` (files without the `integration` build tag) | `make test` (`go test ./... -race`) | nothing |
| Behaviour (godog) | Gherkin scenarios over the real use cases | `features/` | `make bdd` (`go test ./... -run TestFeatures -v`) | nothing |
| Integration | repositories, outbox relay, consumers, migrations and the real `run(ctx)` wiring against real Postgres and Kafka | files with `//go:build integration` (17 files, for example `cmd/api/e2e_integration_test.go`, `internal/adapters/outbound/postgres/relay_store_integration_test.go`, `internal/adapters/inbound/kafka/consumer_integration_test.go`) | `make integration` (`go test -tags=integration ./... -race -count=1`) | Docker (testcontainers boots its own Postgres and Kafka; no `DATABASE_URL`, no compose, no skip gate) |
| Contract | specs against code | see below | part of `make test` | nothing |
| Mutation | do the tests actually assert | `internal/domain/<pkg>` | `make mutation`, `make mutation-full` | gremlins `v0.6.0` |
| Fitness (architecture) | layering and fleet rules as executable tests | `internal/architecture/` | `make arch-test` | nothing |

## Unit and handler tests

Domain packages `internal/domain/scorecard` and `internal/domain/shared` are pure Go and tested
without any adapter. `make coverage` runs `go test ./... -race` with
`-coverpkg=./internal/domain/...,./internal/application/...` and fails below **90%**
(`COVERAGE_THRESHOLD` in the `Makefile`); it is the only definition of the coverage scope and the
`test` CI job runs exactly it. The in-memory adapter `internal/adapters/outbound/memory` and the
shared `repocontract` suite exist for tests only; `TestRuntimeBinaryHasNoInMemoryAdapters` keeps
`cmd/**` from importing them.

## Godog features

`features/` holds 7 feature files with **48** `Scenario` / `Scenario Outline` entries:
`observations.feature` (9), `metrics.feature` (8), `inbound_metrics.feature` (8),
`grading.feature` (7), `rest_reads.feature` (7), `alerts.feature` (5) and
`published_payloads.feature` (4), with step definitions in `features/steps_*_test.go` and the
suite entry point `features/features_test.go` (`TestFeatures`). No `.go` file lives at the repo
root. They run in the `bdd` CI job and as part of `make check-all`.

## Integration tests

17 files carry the `integration` build tag. Notable ones:

- `cmd/api/e2e_integration_test.go`: stops the real entry point through context cancellation and
  asserts the outbox-with-Kafka-down behaviour and the end-to-end consume, grade and publish path.
- `cmd/api/main_integration_test.go`, `cmd/mcp/main_integration_test.go`,
  `cmd/a2a/postgres_integration_test.go`: the three composition roots against real Postgres.
- `internal/adapters/outbound/postgres/*_integration_test.go`: repositories through the shared
  `repocontract` suite, migrations, the relay store (`FOR UPDATE SKIP LOCKED`, attempt cap) and
  trace-header storage.
- `internal/adapters/inbound/kafka/*_integration_test.go` and
  `internal/adapters/outbound/kafka/relay_integration_test.go`: consumers, dead-lettering and the
  relay sink against a real broker.

Two fitness tests police them: `TestPostgresIntegrationTestsUseTestcontainers` and
`TestKafkaIntegrationTestsUseTestcontainers` (no `os.Getenv("DATABASE_URL")` / `KAFKA_BROKERS`
skip gates and no hard-coded `localhost:9092`, because the CI integration job provisions no
broker of its own).

## Contract tests

- REST: `features/steps_reads_test.go` loads `apis/openapi.yaml` and checks the read answers
  against it.
- Events: `internal/adapters/outbound/kafka/encoder_test.go` (golden payloads against
  `apis/asyncapi.yaml`) and `internal/architecture/catalogue_fitness_test.go`
  (`TestEventCatalogueMatchesContract`: the event catalogue in ADR 0004 against the AsyncAPI
  document and the code).
- MCP: `internal/adapters/inbound/mcp/governance_test.go` pins the tool list and annotations.
- CI also lints both documents: the `api-lint` job runs `spectral lint apis/openapi.yaml` and
  `apis/asyncapi.yaml` with `--fail-severity=warn`.

## Mutation tests

`.gremlins.yaml` sets one worker, `timeout-coefficient: 30` and thresholds `efficacy: "95"`,
`mutant-coverage: "95"` (strictly below the measured 100% of both domain packages, 31 of 31 killed
in `shared`, 95 of 95 in `scorecard`). `make mutation` loops gremlins over every package under
`internal/domain/*/`; `make mutation-full` runs the exhaustive `./internal/domain` pass that the
scheduled `mutation` job uses.

## Fitness tests

`make arch-test` runs `internal/architecture/` (arch-go for the layering rule, plain Go AST/text
scans for the rest). Tests ending in `SensorBites` or `...FailsOnBadFixtures` prove that a
fitness rule actually fails on a bad fixture.

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | six dependency rules: the domain imports nothing internal but the domain, the application layer reaches only domain and application, inbound and outbound adapters never import each other |
| `TestMCPAdapterDependencyRule`, `TestMCPOptionalToolInputsCarryOmitempty` | the MCP adapter and `cmd/mcp` stay read-only and never import the REST adapter; optional tool inputs are `omitempty` |
| `TestA2AAdapterDependencyRule`, `TestA2AAgentIsReadOnlyAndHasNoOutboundClient`, `TestA2ADepsCarryOnlyTheTwoReadUseCases` | the A2A agent is read-only, has no outbound client and carries only the two read use cases |
| `TestNoAuthMiddlewareReintroduced` | no bearer, JWT or API-key check under `internal/adapters/inbound` |
| `TestRuntimeBinaryHasNoInMemoryAdapters`, `TestRuntimeBinaryDoesNotImportTheRepositoryContract` | `cmd/**` never imports `internal/adapters/outbound/memory` or `repocontract` |
| `TestNoGoFilesAtRepoRoot` | no `.go` file at the repository root (godog lives in `features/`) |
| `TestKafkaConsumerGroupNeverHardcodedInline` | consumer groups come only from their environment variables |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers` | integration tests boot their own containers |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope` | CloudEvents structured mode only; no toggle or flat envelope |
| `TestEventCatalogueMatchesContract` | ADR 0004 types, AsyncAPI and the encoder agree |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice` | every Kafka writer sets `RequiredAcks` and never a topic on both writer and message |
| `TestOutboxRelaysPassAttemptCap` | every relay is built with the attempt cap |
| `TestReplayConsumersSetCommitInterval`, `TestDeadLetterCallsGuardCancellation` | consumer commit and dead-letter shutdown rules |

## CI jobs (`ci.yml`)

Triggers: push and pull request on `main` and `develop`, a weekly schedule (Monday 06:00 UTC) and
manual dispatch.

| Job | What it runs |
| --- | --- |
| `lint` | golangci-lint `v2.14.0` |
| `guide-lint` | `scripts/harness/guide_lint.py` and `test_hook.py` (advisory, `continue-on-error`) |
| `complexity` | gocyclo, cyclop, gocognit, nestif, funlen through golangci-lint |
| `test` | `make coverage` (race, 90% gate) |
| `bdd` | `go test ./... -run TestFeatures -v` |
| `integration` | `go test -tags=integration ./... -race -count=1` |
| `mutation-fast` | gremlins per `internal/domain` package (blocking) |
| `mutation` | exhaustive gremlins run, scheduled; opens a red issue on failure |
| `drift` | scheduled spec/model drift and coverage-quality checks |
| `api-lint` | Spectral on both specs |
| `vuln` | `govulncheck ./...` |
| `helm-lint` | `ct lint` and the service-selector test (pull requests) |
| `chart-tests` | `charts/node-performance/tests/render_test.sh` and `test_service_selectors.py` |
| `arch-test` | `go test ./internal/architecture/... -v` |
| `web` | `npm ci`, oxlint, `tsc -b`, vitest and the build of `web/` |
| `docker-publish`, `release` | main only: image build and push, chart packaging |

The local gate is `make check` (fmt-check, vet, build, lint, test; also the pre-push hook) and
`make check-all` (adds coverage, arch-test and bdd). `make check-fast` is the agent Stop-hook gate.
`python3 scripts/harness/repo_lint.py` and `python3 scripts/harness/guide_lint.py` must stay clean.
