# Documentation map

Read only what the current task needs.

1. Start with this file.
2. Open [`plan/README.md`](plan/README.md) to find the job.
3. Read the job file. It links the exact architecture sections it depends on.

Precedence when documents disagree: `CLAUDE.md` (product brief) > ADRs in `docs/adr/` (created during E01) > `architecture/` > `plan/`. Fix the lower document.

## Architecture (`architecture/`)

| File | Covers | Read when working on |
| --- | --- | --- |
| [overview.md](architecture/overview.md) | Goals, non-goals, key decisions, components, deployment, process modes, routes, repo layout, glossary | Anything structural; first read for newcomers |
| [connectors.md](architecture/connectors.md) | Execution modes, connector and normalizer contracts, runtime duties, push ingest contract, Withings pattern | Ingestion, sync, connectors, normalizers |
| [data-model.md](architecture/data-model.md) | Tables, IDs, dedupe keys, corrections, measurements, metric catalogue, volume, retention | Migrations, writers, queries |
| [resolution.md](architecture/resolution.md) | Rule schema, windows, aggregation, strategies, sleep alignment, overrides, results, cache | Resolution engine, rule UI |
| [api.md](architecture/api.md) | API conventions, endpoint surface, example payloads | Handlers, OpenAPI, frontend data use |
| [reliability.md](architecture/reliability.md) | Job queue, scheduler, idempotency, upgrades, health, logs, metrics, backups | Jobs, ops, backup |
| [security.md](architecture/security.md) | Threat model, keys, DB roles, network, authorization, deletion | Auth, secrets, anything exposed |
| [apple-health.md](architecture/apple-health.md) | HealthKit bridge: package, app, sync, payload, origins, pairing | E15 |
| [lab-documents.md](architecture/lab-documents.md) | PDF storage, extraction providers, review, privacy | E12 |
| [frontend.md](architecture/frontend.md) | UI stack, navigation, rule builder | E11 |
| [project.md](architecture/project.md) | License, testing, releases, resource budget, deferred features, risks, assumptions, open questions | Release, CI, planning |
| [migration.md](architecture/migration.md), [migration-reference.md](architecture/migration-reference.md) | **Deferred** final epic: design notes and owner-verified reference facts (VPS, Open Wearables, Garmin collector, WHOOP) | Only E16 |

## Plan (`plan/`)

[`plan/README.md`](plan/README.md) is the index: gates, epic order, MVP boundary, and parallel streams. Each epic folder has a `README.md` (objective, acceptance, job list) and one file per job (objective, outputs, done-when, task checklist).
