# Changelog

Newest first. Before a final release, `scripts/release-notes.sh --changelog vX.Y.Z` adds its section from the Conventional Commits since the previous final tag; edit it and add upgrade notes under "Breaking changes" before tagging. The release workflow refuses a final tag without its section and uses it as the release notes. Release candidates are described on their GitHub releases only.

## v0.1.0 (2026-10-04)

First release. Vitamux is a self-hosted personal health data aggregator: it keeps what your sources send, normalizes it, and lets you decide which source wins for each metric.

### Sources

- Connect a Withings account over OAuth from the web UI, with blood pressure and body measures synced incrementally.
- Withings notifications trigger a sync as soon as new measures arrive; subscriptions are renewed for you.
- Resumable backfills, manual and correction syncs, rate-limit and Retry-After handling, and clear states such as needs re-authorization.
- Push ingest API with idempotency keys, gzip bodies, size limits and a heartbeat, for devices and scripts that send their own data.
- Manual entry for single values.
- A small example connector shows how to write your own; the adapter guide walks through it.

### Normalization and provenance

- Original provider payloads are stored first, content-addressed and immutable, so everything can be reprocessed (`vitamux reprocess`).
- A stable metric catalogue with canonical units, conversions and local dates computed in your configured timezones, including timezone changes over time.
- Every normalized value traces back to its raw payload, batch, client and normalizer version.

### Resolution engine

- Per-metric rules choose among sources with single source, first available, mean, min, max, sum, latest and earliest strategies, over hours, local days, sleep episodes and latest windows.
- Sources are aggregated within themselves before being combined, with per-window fallback and quality gates.
- Sleep episodes are aligned before comparison, and workouts from different sources are clustered.
- Built-in default rules ship for every metric; rules are immutable versions, activation is audited, and you can add your own.
- Reversible, audited manual overrides.
- Resolved results explain themselves: inputs, operation, coverage, rule version and a plain-language reason. An all-sources view is always available.
- Hourly aggregates and a resolved cache that invalidates itself; `vitamux resolve verify` checks the cache against a fresh computation.

### API

- Contract-first OpenAPI v1 with a generated TypeScript client and a generated API reference.
- Endpoints for source data and provenance, resolved daily values, series, sleep and workouts, drilldown, rule preview, the metric catalogue, and a coverage matrix.
- Endpoints for rules, overrides, connections, backfills, schedules, settings, timezones, API keys, manual entry and system status.
- Stable keyset paging with signed cursors.
- Streaming zip exports with one-time download tokens, and NDJSON import.

### Web UI

- Sign-in with optional TOTP, a Today dashboard, and connections with an OAuth wizard, syncs and backfills.
- Daily data view with all-sources drilldown, provenance, overrides, sleep and workouts.
- Rules catalogue with version history and a five-step guided rule builder.
- Settings for profile, timezones, API keys, AI providers, retention, backups, security and system.
- Accessibility checks and per-route size budgets in CI.

### Lab documents

- Upload blood-test PDFs to private per-document encrypted storage; deleting a document destroys its key.
- AI extraction is off until you enable a provider and consent to each request: a built-in fake for testing, Gemini, or OpenAI.
- Review rows side by side with the PDF and highlighted evidence; nothing is confirmed without your review.
- Confirmed results keep original labels and units, with revisions, validation checks, unit conversion and an audit trail.
- Lab results export.

### Operations

- PostgreSQL-backed job queue with graceful drain, a scheduler, and connection health and job history.
- Backup and restore (`vitamux backup`, `vitamux restore`) with authenticated manifests, scheduled backups, and a restore drill in CI.
- Retention pruning for raw, superseded and idempotency data, connection deletion, and an admin purge of a whole user.
- Private Prometheus metrics, `/healthz` and `/readyz`, secret-free structured logs, and a measured resource budget.
- `vitamux migrate up`, plus `vitamux admin create-owner` and `vitamux admin init-secrets` for first setup.

### Security

- Master-key vault for tokens and secrets, with documented key rotation.
- Owner login, sessions you can list and revoke, password change, TOTP, API keys and client tokens.
- A threat model and a route-by-principal authorization matrix enforced in CI.
- Sentinel redaction audit that checks secrets never reach logs or responses.
- Hardening: fuzzed parsers, JSON depth and upload limits, least-privilege database roles.
- Supply chain: govulncheck, npm audit, SBOMs, third-party notices with a license allowlist, SHA-pinned actions and digest-pinned images, signed release artifacts.

### Deployment and docs

- Hardened release Docker Compose (multi-arch images, `vitamux healthcheck`) and a Coolify Compose file.
- Install guide, configuration reference, upgrade, key rotation, backup and troubleshooting guides, FAQ and adapter guide in [docs](docs/README.md).

Known limitations: see [docs/release-notes/KNOWN_LIMITATIONS.md](docs/release-notes/KNOWN_LIMITATIONS.md); they are appended to the release notes.
