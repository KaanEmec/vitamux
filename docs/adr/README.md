# Architecture decision records

ADRs record decisions that code depends on. Keep each one short and link to the architecture doc for details. Use [0000-template.md](0000-template.md). Number ADRs as listed in [overview.md › Key decisions](../architecture/overview.md#key-decisions); the remaining ones are written by the jobs that own them.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-go-core.md) | Go core; other languages only in optional sidecars | Accepted |
| [0002](0002-modular-monolith.md) | Modular monolith, one binary with modes, PostgreSQL only | Accepted |
| [0003](0003-postgres-job-queue.md) | Own PostgreSQL job queue: `SKIP LOCKED`, fenced leases, dedupe keys, advisory-lock scheduler | Accepted |
| [0004](0004-blob-store.md) | Filesystem blob store: SHA-256 addressed, HMAC file names, zstd, verified reads, refcount sweep | Accepted |
| [0008](0008-rule-schema.md) | Typed resolution rule schema v1 (`vitamux.rule/1`), first-match groups, sleep and blood-pressure rule families | Accepted |
| [0009](0009-sleep-date-night-window.md) | `sleep_date` = local wake date; night D = sessions ending in `[D−1 18:00, D 18:00)` local | Accepted |
| [0010](0010-sveltekit-static-spa.md) | SvelteKit static SPA embedded in the binary | Accepted, amended by 0020 |
| [0011](0011-secrets-vault.md) | Master key file, HKDF purposes, AES-256-GCM sealed values with `key_id` | Accepted |
| [0012](0012-mit-license-dco.md) | MIT license with DCO sign-off | Accepted |
| [0013](0013-extraction-consent.md) | Extraction providers behind one interface; external ones need configuration, owner enablement and per-request consent naming provider and model | Accepted |
| [0014](0014-healthkit-contract.md) | HealthKit device contract: frozen `healthkit.samples.v1`, raw units, commit-after-ack with `409` as accepted, one `health_events` table | Accepted |
| [0015](0015-rest-openapi-conventions.md) | REST + OpenAPI 3.1 contract-first, problem+json, cursor pagination | Accepted |
| [0016](0016-canonical-writer.md) | Account-scoped dedupe keys; corrections supersede; one canonical writer | Accepted |
| [0017](0017-sidecar-protocol.md) | Sidecar protocol `vitamux-connector/1`: HTTP + JSON + NDJSON pages, frozen, additive only within v1 | Accepted |
| [0018](0018-garmin-upstream.md) | Garmin Connect through a sidecar wrapping `python-garminconnect` | Accepted |
| [0019](0019-whoop-upstream.md) | WHOOP through a sidecar wrapping `@dofek/whoop` (private API, 6 s HR) | Accepted |
| [0020](0020-chart-kit.md) | Hand-written SVG chart kit (lazy chunk ≤ 40 KiB gzip); uPlot removed | Superseded by 0022 |
| [0021](0021-source-setup.md) | Source setup in the panel: sealed provider apps and panel sidecars, environment wins, setup states | Proposed |
| [0022](0022-layerchart.md) | LayerChart for the chart kit (lazy chunk ≤ 150 KiB gzip); supersedes 0020 | Accepted |
| [0023](0023-ios-app.md) | Native SwiftUI iPhone app replacing Bridge: lean rules (two packages, one app target, `AppState`, `Route`), bearer app sessions, fixed `vitamux://` OAuth return, generated client | Proposed |
