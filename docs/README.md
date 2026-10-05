# Documentation map

Read only what the current task needs.

1. Start with this file.
2. Open [`plan/README.md`](plan/README.md) to find the job.
3. Read the job file. It links the exact architecture sections it depends on.

Precedence when documents disagree: `CLAUDE.md` (product brief) > ADRs in `docs/adr/` (created during E01) > `architecture/` > `plan/`. Fix the lower document.

## Install, run, use

| File | Covers | Read when working on |
| --- | --- | --- |
| [install.md](install.md) | Compose install, reverse proxies, first owner, Withings URLs, backups, Coolify | Installing; changing Compose files or setup steps |
| [configuration.md](configuration.md) | Every `VITAMUX_*` variable: default, secret, since (test-enforced against `internal/config`) | Adding or changing a setting |
| [operations/upgrade.md](operations/upgrade.md) | Image bump, migrate, drain, rollback rules | Releases, migrations |
| [operations/key-rotation.md](operations/key-rotation.md) | Master key rotation and what gets re-sealed | Anything sealed with the master key |
| [operations/troubleshooting.md](operations/troubleshooting.md) | `/readyz` failures, schema mismatch, `needs_reauth`, degraded streams, cache verify | Health checks, connection states, error messages |
| [apple/HealthBridgeApp/README.md](../apple/HealthBridgeApp/README.md) | Build the iOS app from source (team, signing, capabilities), pair it, privacy statement, background-timing limits | Installing or changing the Apple Health app |
| [apple-health-device-checklist.md](apple-health-device-checklist.md) | Physical-iPhone test checks and results table for the Apple Health app | Device campaign (J15.7), release sign-off |
| [faq.md](faq.md) | What Vitamux is and is not, data ownership, sources, roadmap | Product questions |
| [api-reference.md](api-reference.md) | Generated: operations by tag with access (`go run ./tools/apiref`) | API consumers; regenerate after spec or `authz.yaml` changes |
| [sidecars.md](sidecars.md) | Wrapping a third-party collector: sidecar or push collector, template, license gate, checks, upstream tracking | Third-party collectors, `sidecars/`, sidecar workflows |
| [adapters.md](adapters.md) | Writing a connector step by step; toy example in `internal/connectors/example` | New connectors and normalizers |

`go test ./tools/doclinks` checks every relative link and anchor in `docs/` and the root `*.md` files.

## Architecture (`architecture/`)

| File | Covers | Read when working on |
| --- | --- | --- |
| [overview.md](architecture/overview.md) | Goals, non-goals, key decisions, components, deployment, process modes, routes, repo layout, glossary | Anything structural; first read for newcomers |
| [connectors.md](architecture/connectors.md) | Execution modes, connector and normalizer contracts, runtime duties, push ingest contract, Withings pattern | Ingestion, sync, connectors, normalizers |
| [data-model.md](architecture/data-model.md) | Tables, IDs, dedupe keys, corrections, measurements, metric catalogue, volume, retention | Migrations, writers, queries |
| [metric-catalog.md](architecture/metric-catalog.md) | Metric code rules, decisions, and codes not yet implemented (HealthKit and Withings mappings, provider scores, events) | Adding catalogue codes |
| [metrics.md](metrics.md) | Generated: implemented metric codes, units, conversions, windows, strategies | Catalogue, normalizers, rules |
| [analyte-catalog.md](architecture/analyte-catalog.md) | Lab analyte code, unit, conversion and alias rules | Adding analytes |
| [analytes.md](analytes.md) | Generated: implemented analyte codes, canonical units, conversions, refused units, aliases | Lab review, conversions |
| [resolution-defaults.md](architecture/resolution-defaults.md) | Suggested built-in rules per metric, brand evidence tiers, sources | Built-in defaults (J09.2), rule UI |
| [resolution-defaults.md](resolution-defaults.md) | Generated: the built-in rules that ship, their groups and coverage | Rules, rule UI |
| [resolution.md](architecture/resolution.md) | Rule schema, windows, aggregation, strategies, sleep alignment, overrides, results, cache | Resolution engine, rule UI |
| [api.md](architecture/api.md) | API conventions, endpoint surface, example payloads | Handlers, OpenAPI, frontend data use |
| [reliability.md](architecture/reliability.md) | Job queue, scheduler, idempotency, upgrades, health, logs, metrics, backups | Jobs, ops, backup |
| [security.md](architecture/security.md) | Threat model, keys, DB roles, network, authorization, deletion | Auth, secrets, anything exposed |
| [security.md](security.md) | Overview: assets, actors, trust boundaries, controls, residual risks | Security reviews, PRs touching anything exposed |
| [apple-health.md](architecture/apple-health.md) | HealthKit bridge: package, app, sync, payload, origins, pairing | E15 |
| [ios-app.md](architecture/ios-app.md) | iOS app: lean architecture, app sessions, native OAuth return, parity matrix, charts, Apple Health and Apple Watch data, cache, widgets, notifications | E22 |
| [ios-app-screens.md](architecture/ios-app-screens.md) | iOS app screen map per tab, sheets, `vitamux://` deep links and the panel route each mirrors | E22 screens |
| [lab-documents.md](architecture/lab-documents.md) | PDF storage, extraction providers, review, privacy | E12 |
| [frontend.md](architecture/frontend.md) | UI stack, navigation, rule builder | E11 |
| [project.md](architecture/project.md) | License, testing, releases, resource budget, deferred features, risks, assumptions, open questions | Release, CI, planning |
| [resource-budget.md](resource-budget.md) | Measured RSS, CPU and disk on the one-year dataset against the budget; deviations; how to re-measure | Resource claims, sizing, release checks |
| [deploy/compose.md](deploy/compose.md) | Release and Coolify Compose files: hardening policy, files and permissions | Deploying, container or Compose changes |
| [operations/backup.md](operations/backup.md) | Backup contents, scheduled backups, off-host encryption, restore steps, drill | Backups, restore, disaster recovery |
| [release-checklist.md](release-checklist.md) | Tagging an rc, artifacts, clean installs, changelog, final tag | Releasing |
| [providers/withings.md](providers/withings.md) | Verified Withings API facts: OAuth, getmeas, meastypes, notifications, limits; how Vitamux syncs | Withings connector (E08) |

## Plan (`plan/`)

[`plan/README.md`](plan/README.md) is the index: gates, epic order, MVP boundary, and parallel streams. Each epic folder has a `README.md` (objective, acceptance, job list) and one file per job (objective, outputs, done-when, task checklist).
