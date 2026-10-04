# Vitamux — project brief

Vitamux is a standalone, open-source (MIT), self-hosted personal health data aggregator. Read this file first, then [`docs/README.md`](docs/README.md), which routes to the smallest relevant doc. Work items live in [`docs/plan/`](docs/plan/README.md) as one file per job.

## Hard rules

- Never put credentials, API keys, access or refresh tokens, passwords, MFA codes, private health payloads, or extracted medical values in source control, logs, prompts, fixtures, or docs. Fixtures are synthetic only.
- Never diagnose, interpret, or advise. Blood-test extraction is structured data entry with provenance and mandatory human confirmation.
- Vitamux is its own product. It is not an Open Wearables plugin, fork, or companion, and it is not specific to any VPS. Reference-environment facts (VPS, its Coolify instance, Open Wearables, existing Garmin collector, WHOOP plans) are only for the final migration epic E16: the owner-local, git-ignored `docs/architecture/migration-reference.md` (not in the repository). Do not read or design for them elsewhere.
- Keep documentation lean: update the smallest relevant file and link instead of repeating. Do not create monolithic docs.
- Precedence: this file > ADRs (`docs/adr/`) > `docs/architecture/` > `docs/plan/`.

## Product objective

Vitamux:

- receives data from official and unofficial wearable or device sources;
- keeps original provider responses for reprocessing;
- normalizes into a stable provider-independent model;
- runs scheduled incremental syncs and resumable backfills;
- stores everything in PostgreSQL;
- exposes APIs for all sources or a selected source;
- resolves each metric and window through user-configurable rules;
- provides a small friendly configurator UI;
- ingests blood-test PDFs with configurable AI extraction and human review.

It must stay understandable for a small community.

Scope:

- MVP connectors: Withings (official OAuth; blood pressure first), push ingestion, and file imports.
- Apple Health bridge: required (epic E15).
- Any source, including existing open-source collectors in other languages, plugs in through the connector contract: remote sidecars or push collectors (E17, v0.2.0).
- Garmin Connect (unofficial, wraps `python-garminconnect`) and WHOOP (unofficial, wraps `@dofek/whoop`): sidecar epics E18 and E19 (v0.3.0). Wrapped upstreams follow their releases automatically, gated by tests ([J17.5](docs/plan/E17-sidecar-connectors/J17.5-upstream-tracking.md)).
- The existing reference Garmin collector and the Open Wearables migration: deferred to E16.
- Future sources (Ultrahuman, scales, BP devices, other official or unofficial adapters) must fit the same connector contract.
- Single owner first. `user_id` exists on durable rows, but there are no orgs, billing, or role systems.
- Not in scope: analytics suite, social, coaching, clinical portal.

## Decided technical direction

These decisions are recorded in [`docs/architecture/overview.md`](docs/architecture/overview.md#key-decisions).

- **Go** modular monolith: one binary `vitamux` with modes `serve`, `migrate`, `import`, `backup`, …
- **PostgreSQL** is the only database. Jobs, locks, and schedules live in PostgreSQL. No Redis, Kafka, Celery, Kubernetes, workflow engine, or microservices without evidence.
- **SvelteKit** static SPA embedded in the binary.
- **Docker Compose** is the reference deployment; **Coolify** is the second supported install target (a Compose variant; generic, no reference-VPS values).
- Other languages only in optional, replaceable sidecars behind the connector contract. Existing open-source collectors are reused as sidecars or push collectors ([E17](docs/plan/E17-sidecar-connectors/README.md)).

## Data requirements (non-negotiable)

- Preserve raw authorized source data before transforming it. Source records are immutable or versioned; corrections are auditable replacements.
- Every normalized record can answer:
  - person; provider, connection, device, origin app, collector;
  - metric, value, canonical and source unit;
  - timestamp, local date, timezone or offset;
  - kind (sample, interval, cumulative, provider daily total);
  - external id or dedupe key;
  - fetched, normalized, corrected, and superseded times;
  - supporting raw payload or batch; normalizer version.
- Sleep, workouts, blood pressure, and lab results are events or groups, not forced into one scalar table.
- Local dates use the person's configured timezone, never UTC alone.
- Never overwrite normalized source data with calculated results. Resolution is dynamic or cached by rule version, and is reproducible.

## Resolution requirements

Per metric or event type, use versioned typed rules (no expression language). Supported:

- strategies: `single_source`, `first_available`, `mean`, `min`, `max`, `sum` (explicit, with a duplicate-risk warning), `latest`/`earliest`, event selection;
- selectors at provider, origin app, connection, device-type, and device level;
- windows: aligned bucket, hour, local day, local night or sleep episode, latest.

The engine must:

- aggregate within each source before combining across sources, so high-frequency sources don't dominate;
- align sleep episodes before comparing them;
- never add daily totals to their intraday components;
- fall back per window only when the strategy calls for it;
- return inputs, operation, coverage, rule version, and a human-readable explanation;
- keep an all-sources view;
- support reversible, audited manual overrides.

Details are in [`docs/architecture/resolution.md`](docs/architecture/resolution.md).

## Connectors and scheduling

Support:

- interactive auth bootstrap; protected token storage and refresh;
- guided setup in the web panel for every source: OAuth app credentials, keys, sign-in and MFA, with no file edits or restarts beyond turning on an optional sidecar ([E20](docs/plan/E20-guided-setup/README.md));
- incremental, manual, and correction syncs; bounded resumable backfill; idempotent replay;
- rate limits, Retry-After, backoff, and jitter;
- per-connection health;
- secret-free structured logs;
- a clear failure state when an unofficial endpoint changes shape (never silently substitute other data).

## Apple Health

A backend cannot read HealthKit. Use a reusable Swift package plus a minimal SwiftUI app with:

- per-type read authorization;
- anchored incremental sync with deletions;
- background delivery (system-controlled; test on a physical device);
- secure pairing; idempotent batch upload.

Preserve UUID, source revision and bundle id, device, timestamps, timezone, metadata, and anchor and batch provenance. Rules must be able to select Apple Health generally or specific origins and devices (e.g., Apple Watch first, exclude Garmin relayed via HealthKit). An export importer is a fallback, not equivalent to sync.

## Blood tests

- Private PDF storage with checksum and retention.
- Pluggable extraction (Gemini, OpenAI, deterministic test double; no vendor lock-in). Sending a PDF externally is explicit and configurable.
- Schema-constrained rows (name, result, unit, range, printed flag, dates, lab, page, evidence) with confidence and warnings.
- Mandatory human review before confirmation; preserved extraction responses, versions, and audit trail; original labels and units kept.

## Operations and open source

- Contributor-friendly setup, versioned migrations, synthetic fixtures, tests, and a sample Compose file.
- Private DB ports; expose only the UI, API, OAuth callbacks, and webhooks.
- Backup and restore; health and readiness endpoints; basic diagnostics; documented resource budgets.

## Architecture principles

1. Raw first.
2. Deterministic, versioned normalization.
3. Selection separate from ingestion and storage.
4. Every selected value is explainable.
5. Restartable, idempotent sync and migration.
6. Unofficial behaviour isolated behind replaceable adapters.
7. Modular monolith + PostgreSQL.
8. One owner and a small community before scale.
9. Standalone first, safe rollback during migration.
10. Never trade away granular data for a simpler schema.
