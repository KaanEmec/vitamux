# Changelog

Newest first. Before a final release, `scripts/release-notes.sh --changelog vX.Y.Z` adds its section from the Conventional Commits since the previous final tag; edit it and add upgrade notes under "Breaking changes" before tagging. The release workflow refuses a final tag without its section and uses it as the release notes. Release candidates are described on their GitHub releases only.

## v0.3.1 (2026-10-05)

Resolution defaults and visible data ([E24](docs/plan/E24-resolution-visibility/README.md)), catalogue completeness and mapping corrections ([E25](docs/plan/E25-catalogue-mappings/README.md)), and intraday Day views ([E26](docs/plan/E26-intraday-views/README.md)).

### Fixed behaviour

- **No phantom zeros.** A device that never reports a metric (a WHOOP band has no active energy or intraday steps) no longer stands in for it with a 0 on worn days.
- **Default rules.** Every metric without a built-in or owner rule resolves through a default rule: your source order (Settings › Source priority), then a generic device ladder; scores take their own provider. Nothing shows `no_data` only because no rule exists, and the all-sources overlay shows even when the resolved series is empty. The first edit copies it into your version 1.
- **Opt-in gates.** The built-in wear and coverage gates are off; turn them on per rule (the Rules page shows an opt-in strip). A daily total is no longer rejected by an 80 % coverage gate on a partly worn day.
- **Heart rate.** WHOOP ranks directly above Garmin in the built-in heart-rate ladder (owner decision; yours stays as you set it).
- **Withings devices.** Live `getmeas` names the model `modelid`; devices now get their type, so the blood-pressure and scale groups match. Records a phone app relayed (model ids 1051-1060, activity brand 18) are flagged as relayed and never counted twice.
- **WHOOP sleep stages.** Deep, awake, disturbances and latency come from the real stage events.
- **Garmin SpO2.** The per-minute sleep readings become `spo2` samples and the nightly average `spo2_nightly`.
- **Stream reconciliation.** A connection follows the streams its connector declares now: retired streams are dropped and new ones scheduled when a sidecar describes itself, and an owner-disabled stream no longer keeps the connection degraded. Sidecars cut days at your local midnight.

### New codes

New catalogue codes cover everything the providers send: sleep awakenings and temperature deviation, intensity and sedentary times, sport-specific distances, speeds, powers and cadences, urine and ECG values, WHOOP sleep need and heart-rate zones, Garmin activity summary values, and the Apple Health types that had none. Every code, with its unit and mapping, is in [docs/metrics.md](docs/metrics.md); values that stay raw have a recorded reason in each provider's field ledger.

### New streams

- **Withings** activity, intraday activity and sleep. They need the new `user.activity` scope: **reconnect Withings** (Connections › Withings › Reconnect) to grant it.
- **Garmin** floors, hydration and fitness age; training status adds acute and chronic load; an opt-in, paced **intraday reload** asks Garmin to restore days it moved to cold storage (a daily budget below Garmin's limit, stops when denied).
- **WHOOP** body measurements.

### Web UI

- **Day view** on every metric that has an intraday ladder (heart rate to 30-second buckets and raw readings, steps as 30-minute bars): date stepper, zoom through the ladder, min-max band, per-source series, night and workout overlays.
- Source series fall back to the all-sources view when a rule resolves nothing, and a total energy card was added to the dashboard.

### API

- `GET /resolved/series`: `window` accepts `30s` and `1m` (up to a day per request; a day of 6-second heart rate resolves in under 100 ms, see [resource-budget](docs/resource-budget.md#intraday-series)).
- `GET /sources/series`: `grain` accepts `30s`, `1m`, `5m`, `15m`, `30m` and `raw`; raw pages hold at most 2,000 points.
- `GET /metrics/{code}`: `intraday` (default and finest grain). `GET/PUT /settings`: `sources.priority`.

### Breaking changes

No data is removed. Migrations 00032-00041 run on start. Upgrade steps:

1. Pull the new core and sidecar images (`vitamux`, `vitamux-sidecar-garmin`, `vitamux-sidecar-whoop`; the sidecars share the core's tag, or `:stable`). Garmin and WHOOP need the new images for their new streams.
2. Reconnect Withings for the new scope, then run its new streams' backfills.
3. Renormalize stored data (add `--wait` to see each result; on Coolify use `docker exec <vitamux container> /vitamux …`), one line per normalizer whose version changed (Apple Health 2 to 3, Withings measures 1 to 3, WHOOP and Garmin below):

   ```sh
   docker compose exec vitamux /vitamux reprocess --normalizer healthkit.samples
   docker compose exec vitamux /vitamux reprocess --stream withings.measures
   docker compose exec vitamux /vitamux reprocess --stream whoop.heart_rate
   docker compose exec vitamux /vitamux reprocess --stream whoop.cycles
   docker compose exec vitamux /vitamux reprocess --stream whoop.sleep
   docker compose exec vitamux /vitamux reprocess --stream whoop.workouts
   docker compose exec vitamux /vitamux reprocess --stream whoop.strain_deep_dive
   docker compose exec vitamux /vitamux reprocess --stream garmin.daily_summary
   docker compose exec vitamux /vitamux reprocess --stream garmin.steps
   docker compose exec vitamux /vitamux reprocess --stream garmin.sleep
   docker compose exec vitamux /vitamux reprocess --stream garmin.hrv
   docker compose exec vitamux /vitamux reprocess --stream garmin.spo2
   docker compose exec vitamux /vitamux reprocess --stream garmin.training
   docker compose exec vitamux /vitamux reprocess --stream garmin.body_composition
   docker compose exec vitamux /vitamux reprocess --stream garmin.activities
   ```

4. Built-in rule versions changed: steps, distance and active energy `:4` (no gates), heart rate `:3` (WHOOP above Garmin), resting heart rate nocturnal `:3` and sleep `:3` (no coverage gates); total energy is new (`:1`, follows active energy). Rules you edited keep their copy and are untouched; open Rules to compare or reset to the new default.

## v0.3.0 (2026-10-04)

Charts and metric visualisation redesign ([E23](docs/plan/E23-chart-redesign/README.md)).

### Web UI

- **Charts** are drawn with LayerChart ([ADR-0022](docs/adr/0022-layerchart.md), replacing the own SVG kit). Every chart has a crosshair tooltip with the value, status and source, which can be pinned to open Explain, Override or Raw records. Period changes animate (no motion when reduced motion is set), touch can scrub through points, and keyboard navigation and "Show as a table" work as before.
- **Design tokens v3**, dark first: each metric has its own hue and icon tile, and sleep stages and source chips are recoloured. The light theme mirrors every token.
- **Dashboard**: hero tiles (up to four, chosen in edit mode) drive one large chart with 7D/30D/90D/1Y, a mean line, the range and a neutral delta against the previous period. There is a last-night card, and pinned cards with sparklines. Warnings and errors can be **dismissed**; a new occurrence shows again, and "Show" brings dismissed ones back.
- **Metric detail**: a stats header, per-source overlay toggles, "Compare previous", fallback and override markers, shaded gaps, the source behind each day, a brush navigator, a distribution and a values table. The rule lens is a side panel with a draft-vs-active preview.
- **Sleep, blood pressure and body**:
  - Sleep has a last-night hypnogram, nightly stage stacks, bedtime and wake bars, the average night and a list of nights.
  - Blood pressure shows one dumbbell per session, with a morning and evening filter.
  - Body shows weight with a 7-day moving average and composition tiles, with no class labels.
- **Controls** are restyled across every page: buttons, fields, dropdowns, checkboxes, radios, a new switch, tabs, dialogs with action footers, popovers and alerts. Connections, setup, Rules, Lab, Settings and login use them.

### API

- `GET /resolved/series`: `points[].providers`.
- `GET /resolved/sleep`: `nights[].episode` (bed to wake).
- `GET /resolved/summary`: `compare=true` adds `comparisons` (7/30/90/365 days, against the period before).
- `GET/PUT /settings/dashboard`: `hero` (up to four metrics) and `dismissed` (alert keys).

### Breaking changes

None. No migrations. The web build adds `layerchart`, and its chart code loads lazily (about 128 KiB gzip).

## v0.2.8 (2026-10-04)

### Rules

- New **Brand** selector (`device_manufacturer`, case-insensitive) next to device type and model. A brand matches its devices on every path, e.g. a Garmin watch synced directly and through Apple Health; add "Not relayed" to narrow it.
- The rule builder and the rule lens offer **Choose a source or device**: Apple Health (all data), Apple Watch, iPhone, each connected source (all data), each brand (any device), each brand and model, and devices you named. Merged devices are left out. Built-in groups show friendly names such as "Apple Watch" and "Garmin via Apple Health".
- Built-in defaults use the named groups: `apple_watch` is Apple's own Watch data by manufacturer and model, a new `iphone` group comes before the generic phone in the steps, distance and energy ladders, WHOOP, Polar and Fitbit gain their Apple Health relay groups, and Garmin and Withings relays also match by manufacturer. Brand ranks are unchanged ([defaults](docs/resolution-defaults.md)). New built-in versions: steps, distance and active energy `:3`; heart rate, resting heart rate, HRV, SpO2, respiratory rate, sleep, VO2max, pulse wave velocity and vascular age `:2`. Rules you already edited keep their copy.

### Breaking changes

None. Migration 00031 lets a device's manufacturer change clear cached results.

## v0.2.7 (2026-10-04)

### Web UI

- Connections › <connection> › **Devices**: set each device's type (watch, band, ring, phone, scale, …) and name, and **merge** a device into another, e.g. Garmin's wellness stand-in into the watch that records workouts. A merge moves all of the device's records, and later syncs land on the target. A type you set is not overwritten by syncs.

### API

- `PATCH /api/v1/source-devices/{id}` and `POST /api/v1/source-devices/{id}/merge`. `GET /api/v1/source-devices` adds `name`, `merged_into`, the connection and, with `include=records`, record counts.

### Connectors

- Garmin VO2max is attributed to the Garmin wearable like the other wellness data (`garmin.training` v4).

### Breaking changes

None. Migration 00030 adds the device columns. To move existing Garmin VO2max rows: `vitamux reprocess --normalizer garmin.training`.

## v0.2.6 (2026-10-04)

### Connectors

- Garmin wellness data has a device: heart rate, steps, stress, Body Battery, HRV, respiration, SpO2, daily totals, sleep and training. Garmin's responses don't name the wrist device, so these rows had none, matched no device-type group, and were left out of `builtin:steps` and other device-type rules. They now use one stable device, "Garmin wearable", of type `watch`. Scale and blood-pressure readings and activities keep the device Garmin names. Garmin's wellness normalizers are bumped (daily summary and training to 3, the others to 2).

### Breaking changes

None. To attach the device to Garmin data already stored, reprocess every Garmin wellness stream, e.g. `vitamux reprocess --normalizer garmin.steps` and the same for `garmin.heart_rate`, `garmin.daily_summary`, `garmin.stress_body_battery`, `garmin.sleep`, `garmin.hrv`, `garmin.respiration`, `garmin.spo2` and `garmin.training`.

## v0.2.5 (2026-10-04)

### Connectors

- WHOOP daily steps: read from the strain deep dive (`CONTRIBUTORS_TILE_STEPS`) as a daily total per local day. WHOOP no longer serves intraday steps. `whoop.strain_deep_dive` is now normalizer version 2.

### Breaking changes

None. To get daily steps from deep dives already stored: `vitamux reprocess --stream whoop.strain_deep_dive`.

## v0.2.4 (2026-10-04)

### Connectors

- A stream a connector no longer declares (such as `whoop.steps` after v0.2.3) is retired on its next scheduled run. Its schedules stop, its degraded mark is cleared, and the connection returns to active once no other stream is degraded.

### Breaking changes

None.

## v0.2.3 (2026-10-04)

WHOOP now matches the responses of a live account.

### Connectors

- WHOOP cycles and sleep follow WHOOP's real response shapes:
  - recovery, day strain, resting heart rate, nightly RMSSD, and sleep performance and respiratory rate come from cycles;
  - sleep sessions and stages come from each sleep's stage events, naps included.
  - `whoop.cycles` and `whoop.sleep` are now normalizer version 2.
- WHOOP steps are removed: WHOOP's metrics endpoint now serves heart rate only.
- WHOOP strain deep dive is stored raw only and no longer shows as a failed normalization.
- When a connector drops a stream, its schedules are disabled instead of failing the whole connection.

### Breaking changes

None. After upgrading a WHOOP connection made with v0.2.2:

1. Run a `whoop.sleep` backfill over your history. Sleep stored by v0.2.2 lacks the nap flag and normalizes only after it is fetched again.
2. Run `vitamux reprocess --stream whoop.cycles`, `--stream whoop.sleep` and `--stream whoop.strain_deep_dive`.

## v0.2.2 (2026-10-04)

Fixes for the Garmin and WHOOP connectors.

### Connectors

- WHOOP: sign-in now accepts codes WHOOP sends by email. They were refused as wrong codes before, because the upstream client answered them as SMS codes. The sidecar image carries this fix; redeploy to pull the new `:stable` image.
- Garmin: days without wellness data and training-readiness snapshots without a score are stored as empty days instead of failing as schema drift. `garmin.daily_summary` and `garmin.training` are now normalizer version 2.

### Breaking changes

None. To renormalize Garmin days that failed before this release: `vitamux reprocess --normalizer garmin.daily_summary` and `vitamux reprocess --normalizer garmin.training`.

## v0.2.1 (2026-10-04)

Every source is set up from the web panel (epic E20), built on the v0.2.0 design system.

### Web UI

- Connect a source: one card per provider with its setup state and the next step. It is the Connections page on a fresh install, and the Dashboard leads to it.
- Withings: a three-step wizard shows the callback URL to register, takes the client id and secret, checks them and starts the sign-in. No `.env` edit or restart.
- Garmin and WHOOP: the panel shows the one line that turns the sidecar on (Compose or Coolify) and a Check again button, then one sign-in dialog with email, password and MFA code.
- Settings › Sources: replace or delete app credentials, and add your own sidecar (its shared secret is shown once).
- Plain-language errors for a wrong secret, a callback mismatch, an http public URL, a refused sign-in, rate limits and a sidecar that is not running.

### API

- `GET /api/v1/providers` adds `setup_state`, `callback_url`, `problems`, `app_credentials`, `sidecar` and `connections`.
- `PUT`, `DELETE` and `POST …/verify` on `/api/v1/providers/{provider}/app-credentials`, and `POST /api/v1/providers/{provider}/probe`.
- `GET`, `POST` and `DELETE /api/v1/sidecars` for sidecars added in the panel.
- `POST /api/v1/providers/{provider}/auth/continue` answers 422 when the provider refuses the sign-in and 429 with `Retry-After` when rate limited.

### Deployment

- App credentials entered in the panel are sealed in PostgreSQL and covered by key rotation, backup and restore. An environment value still wins and shows as managed by the environment.
- The bundled Garmin and WHOOP sidecars are registered by default and get their shared secrets on start. Turn one on with `COMPOSE_PROFILES=garmin,whoop` (Compose) or `GARMIN_SIDECAR=1` / `WHOOP_SIDECAR=1` (Coolify).

### Breaking changes

None. The Withings environment variables are now optional overrides of the panel value.

## v0.2.0 (2026-10-04)

The web panel is redesigned (epic E21): a dashboard, Explore for everything Vitamux has stored, and rules you can change while looking at the data.

### Web UI

- Design system v2: light and dark themes (system, or chosen in the top bar), Geist fonts served by Vitamux, data-status shapes and stable source colours. A new shell with a collapsible sidebar, a ⌘K / Ctrl+K command palette and a sync-status pill.
- Dashboard replaces Today: metric cards with value, neutral delta against the 30-day mean, sparkline, sources and status. Pin, reorder, resize (S/M/L) and hide cards; the layout is stored on the server. Past days via the date selector.
- Explore replaces Data: every metric, group, event type and lab analyte with data, filterable by provider, device and origin. Metric pages pick their chart from the catalogue, with range and zoom, a 30-day band, each source's own values, coverage, statistics and a values table; every point opens its explanation, provenance and override actions. Old `/data` links redirect.
- Rule lens: reorder sources and change strategy, window or coverage beside the chart, see the draft overlaid with the changed days before saving, then save, activate or revert. Every change is a rule version.
- Views for sleep (stages, bed and wake times, nights across sources), blood pressure, body composition, workouts (calendar and clusters), health events and lab analyte trends (printed ranges only).
- Redesigned Connections (cards with a 14-day run strip and the next action), Rules (rules as sentences, version timeline, the builder as a stepper with a live preview), Lab (split-view review with Enter, J/K and E shortcuts) and Settings (grouped navigation).
- Charts are a small SVG kit that works under the strict CSP and replaces uPlot (ADR 0020); each has a keyboard and table fallback.

### API

- `GET /api/v1/inventory`, `GET /api/v1/event-types` and `GET /api/v1/events`.
- `GET /api/v1/resolved/summary`, `GET /api/v1/resolved/trend` (beyond 366 days, by week or month) and `GET /api/v1/sources/series` (each source's values), as display rollups over the resolution engine.
- `GET` and `PUT /api/v1/settings/dashboard`, a versioned layout stored in settings.

### Fixes

- The rule builder no longer fails when the rule in effect has `contexts`.

### Breaking changes

None. Bookmarks to `/data` pages redirect to Explore.

## v0.1.1 (2026-10-04)

First final release. v0.1.0 was not published as a tag of its own: v0.1.1 contains all of it (its notes are in CHANGELOG.md) plus the changes below.

### Sources

- Apple Health bridge: a Swift package and a minimal iOS app pair with Vitamux and upload HealthKit data incrementally (anchored sync with deletions, idempotent batches). The Apple Health export importer remains as a fallback.
- Sidecar connectors (protocol `vitamux-connector/1`, ADR 0017): run a collector written in any language as its own container behind the connector contract. Configure them with `VITAMUX_SIDECARS`; `vitamux admin init-secrets` creates their secrets.
- Garmin Connect (wraps `python-garminconnect`) and WHOOP (wraps `@dofek/whoop`) as unofficial sidecars, with sign-in and MFA in the web UI. Unofficial connections start paused.
- `vitamux connector-test` conformance kit and a Python example sidecar for writing your own.
- `vitamux import batches [--dry-run] DIR` replays ingest batch files, such as an old collector's archive, into the live connection for the same account without duplicates.
- Sidecar images follow their upstream releases once the checks pass.

### Web UI

- One connection wizard for OAuth redirects and for sign-in or MFA prompts. Provider names and the wrapped upstream version are shown.

### API

- `GET /api/v1/providers`, and `POST /api/v1/providers/{provider}/auth/continue` for prompt-based sign-in. It replaces `POST /api/v1/connections/{id}/auth/continue`, which was never implemented.
- Connections carry `upstream` (wrapped package and version) for sidecar sources.

### Fixes

- Exports import back with providers matched by code, including health events.
- Concurrent normalization no longer fails when two workers insert the same device, origin or normalizer version.
- Sleep-episode windows keep the owner's timezone.
- Resolution aggregates hours in SQL and loads data in a sliding window, which keeps bulk work inside the memory budget.
- Coolify: the public URL comes from `SERVICE_URL_VITAMUX`, and the empty restore command is gone.

### Maintenance

- Go code updated to Go 1.27 idioms with the modernize linter enforced; internal refactors without behaviour changes.
- Dependency updates: kin-openapi 0.144.0 and cel-go 0.29.0 (security advisories; both are build-tool dependencies, not part of the binary), golang.org/x/image 0.46.0, GitHub Actions on the Node 24 runtime.

### Breaking changes

None. Database migrations 00023 to 00028 run with `vitamux migrate up` as for any upgrade.

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
