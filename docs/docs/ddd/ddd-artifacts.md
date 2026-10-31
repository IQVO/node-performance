---
id: ddd-artifacts
title: DDD artifacts
sidebar_label: DDD artifacts
sidebar_position: 3
description: "The ddd-crew artifacts of Node Performance (core domain chart, canvases, context map, message flow, EventStorming, diagrams), checked against the code."
---

# DDD artifacts

The design of Node Performance in the [ddd-crew](https://github.com/ddd-crew) formats, plus the UML and
persistence views that complement them. Every page was written against the code on `develop`; each
diagram carries a `Source:` line naming the files it was drawn from. The shared network model is in
[warehouse-docs](https://iqvo.github.io/warehouse-docs/network/contexts/node-performance/).

| Page | What it holds | Format |
| --- | --- | --- |
| [Core domain chart](/docs/ddd/core-domain-chart) | Why this context is Supporting | [core-domain-charts](https://github.com/ddd-crew/core-domain-charts) |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | Purpose, roles, inbound and outbound messages, decisions, assumptions | [bounded-context-canvas](https://github.com/ddd-crew/bounded-context-canvas) v5 |
| [Context map](/docs/ddd/context-map) | Every upstream and downstream neighbour, pattern and channel | [context-mapping](https://github.com/ddd-crew/context-mapping) |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | The `NodeScorecard` aggregate | [aggregate-design-canvas](https://github.com/ddd-crew/aggregate-design-canvas) v1.1 |
| [Domain message flow](/docs/ddd/domain-message-flow) | Scenarios as commands, events and queries across contexts | [domain-message-flow-modelling](https://github.com/ddd-crew/domain-message-flow-modelling) |
| [EventStorming](/docs/ddd/eventstorming) | The process-level board with policies and read models | [eventstorming-glossary-cheat-sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | The terms and their code identifiers | glossary |
| [Class diagram](/docs/ddd/class-diagram) | The domain model and the hexagonal ports and adapters | UML |
| [Entity relationship](/docs/ddd/entity-relationship) | The Postgres schema | persistence view |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | Adapter, use case, aggregate, outbox, Kafka | UML |
| [Domain events](/docs/ddd/domain-events) | The two published and 15 consumed CloudEvents types | message catalogue |

See also [Use cases](/docs/ddd/use-cases) and [Subdomain classification](/docs/ddd/subdomain-classification).
