# API

## Conventions

- REST + JSON, contract-first in `api/openapi.yaml` (OpenAPI 3.1). Server types (`oapi-codegen`) and the TS client are generated; contract tests validate responses.
- `/api/v1` is for the owner and `/api/ingest/v1` for clients and devices. Only additive changes within v1.
- Instants are RFC 3339 **with offset**, half-open `[start, end)`. Dates are `YYYY-MM-DD` (`start_date`/`end_date`, inclusive), in the user's timezone periods.
- Cursor pagination: `limit` (default 500, max 10,000 for measurements) and an opaque HMAC-protected `cursor`. Responses carry `next_cursor` and `has_more` beside an array named after the resource (`{"measurements": [...], "has_more": true, "next_cursor": "…"}`). No offsets. A cursor is bound to the operation and filters that produced it, and is invalid (422) after tampering or a master-key rotation.
- Repeatable filters: `metric`, `provider`, `connection` (`conn_…`), `device` (`dev_…`), `origin` (origin key), `kind`; time ranges via `start`/`end` or local `start_date`/`end_date`. Expansions via `include=provenance|stages|segments|superseded|deleted`. Source lists return active rows unless `include=superseded` (history) or `include=deleted` (upstream deletions), and page by keyset on (start, id), so concurrent inserts never shift a page.
- Errors are `application/problem+json` with `type`, `title`, `status`, `detail`, `code`, `request_id`, and `errors[]`. Codes: `validation_failed`, `not_found`, `conflict`, `rate_limited`, `reauth_required`, `consent_required`, `unsupported_window`, `rule_warning_unacknowledged`. Also `unauthenticated` and `totp_required` (401), `forbidden` (403), `payload_too_large` (413), `internal_error` (500) and `unavailable` (503). The registry is `internal/api/problem.go`.
- Request bodies are capped per route class (`bodyClasses` in `internal/api/middleware.go`): 1 MiB owner JSON, 10 MiB ingest batches, 25 MiB uploads, 64 KiB for `/api/ingest/v1/devices/` and everything else. JSON bodies nest at most 64 levels (`maxJSONDepth`; ingest checks after gunzip): 422 `validation_failed`. Gzip ingest bodies are also capped at 50 MiB and 100× their compressed size (413). There is no 415: a JSON endpoint reads the body whatever its `Content-Type`, so a wrong type fails as invalid JSON (422), and the document upload answers 422 for any type but multipart or `application/pdf`. `X-Forwarded-For`/`-Proto` are trusted only from `VITAMUX_TRUSTED_PROXIES` (comma-separated CIDRs; empty trusts none).
- `Idempotency-Key` (1–255 characters) is required on ingest batch and blob POSTs and accepted on job-creating owner POSTs. The same key with the same request (method, path, decompressed body) replays the stored response with `Idempotent-Replayed: true`; with a different request it is `409 conflict`. Failed requests store nothing.
- Auth:
  - UI: session cookie (`HttpOnly; Secure; SameSite=Strict`) + `X-CSRF-Token` header on mutating requests (value from `POST /auth/login` or `GET /auth/session`).
  - API: `Bearer vmx_pat_<id>_<secret>` with scopes `read:health`, `read:config`, `write:config`, `write:documents`, `admin` (implies the others).
  - Ingest: `Bearer vmx_cli_<id>_<secret>` with scope `ingest:<connection_id>`; rejected on `/api/v1`.
  - `<id>` is the row UUID as 32 lowercase hex characters; `<secret>` is 32 random bytes, unpadded base64url. Only SHA-256(secret) is stored; the token is shown once.
  - Each route declares its access (public, session, scope, ingest) when registered in `internal/api`; anything else gets 401/403.

## Ingest endpoints (`/api/ingest/v1`)

| Endpoint | Purpose |
| --- | --- |
| `POST /batches`, `POST /batches/{id}/blobs`, `GET /batches/{id}` | Push raw items ([connectors.md](connectors.md#push-ingest-contract)) |
| `POST /heartbeat` | Client checkpoint and health |
| `POST /devices/pair` (public: the pairing code is the credential), `GET /devices/self` (pending anchor resets), `POST /devices/self/rotate-token` | Device pairing and config ([apple-health.md](apple-health.md#pairing-and-security)) |

## Owner endpoints (`/api/v1`)

| Area | Endpoints |
| --- | --- |
| Catalogue | `GET /metrics`, `GET /metrics/{code}` (unit, aggregation, kinds, windows, group, family and the `intraday` ladder: enough to pick a chart), `GET /event-types` |
| Source data | `GET /measurements`, `/groups?kind=`, `/blood-pressure`, `/sleep[/{id}]`, `/workouts[/{id}]`, `/events?code=` (health events), `/provenance/{entity}/{id}`; `GET /inventory` (per metric, group kind, event code, sleep, workouts and lab analyte with data: count, days, first and last seen, latest value, providers, devices, origins and catalogue metadata; metric counts come from the hourly aggregates plus daily values and lag while `aggregates_pending`); `GET /sources/series?metric&start&end&grain=hour\|day` (each source's own values from the hourly aggregates, plus daily values per day, with its place in the rule; `behind` while marks wait for the rebuild) and `grain=30s\|1m\|5m\|15m\|30m\|raw` for spans of at most a day, read from `measurements`, with each source's native `spacing_s`; `raw` pages rows by cursor, at most 2,000 per page |
| Resolved | `GET /resolved/daily`, `/resolved/series?metric&start&end&window`, `/resolved/sleep`, `/resolved/workouts`, `/resolved/{metric}/{window_key}/sources`; `POST /resolution/preview` (draft rule × range; no writes); `GET /resolved/summary?metrics&date&compare` (value of the date, 30 daily values, 7/30/90-day rollups; `compare=true` adds `comparisons`: the 7, 30, 90 and 365-day rollups beside the period before each, for neutral deltas) and `/resolved/trend?metric&start_date&end_date&grain=week\|month` (up to 3,660 dates): [display rollups](resolution.md#display-rollups) |
| Rules and overrides | `GET /rules`, `GET /rules/{metric}/versions`, `POST /rules/{metric}/versions`, `POST /rules/{metric}/activate`, `GET/POST /overrides`, `POST /overrides/{id}/revoke` |
| Coverage and health | `GET /coverage?start_date&end_date&metric&origin` (origin keys: only the hours those apps contributed), `GET /system/status`, `GET /system/version`, `GET /jobs` |
| Connections | `GET/POST /connections` (POST: push connections only), `GET/PATCH/DELETE /connections/{id}` (PATCH pauses/resumes; `?data=keep` disconnects, `?data=delete` also removes its raw payloads, canonical rows, cursors, schedules and jobs, 409 while one of its jobs runs), `GET /providers` (with setup state: [source setup](connectors.md#source-setup-in-the-web-panel)), `PUT/DELETE /providers/{provider}/app-credentials` (write-only; DELETE `?confirm=true` while connections use them), `POST /providers/{provider}/app-credentials/verify`, `POST /providers/{provider}/probe`, `GET/POST /sidecars` (POST returns the generated secret once), `DELETE /sidecars/{name}`, `POST …/auth/begin`, `POST /providers/{provider}/auth/begin\|continue` ([authorization flow](connectors.md#oauth-connection-flow)), `POST …/sync` (one job per stream, coalesced with a pending one), `GET/POST …/backfills`, `GET …/backfills/{id}`, `POST …/backfills/{id}/retry\|cancel`, `GET …/runs`, `GET …/streams`, `POST …/streams/{s}/reset-cursor`, `GET /schedules`, `PATCH /schedules/{id}` |
| Manual data | `POST /measurements/manual`: stored as a raw payload (stream `manual.measurements`) of the owner's provider-`manual` connection and normalized by `normalize.Manual` (flag `manual_entry`; grouped metrics such as weight become a one-component reading), audited |
| Devices | `POST /devices/pairing-codes` (code, expiry, public URL, QR payload; 429 past 5 per 10 minutes), `GET /devices` (name, last seen, last batch, checkpoint types, `possibly_denied`, anchor resets, revoked), `POST /devices/{id}/request-anchor-reset`, `POST /devices/{id}/revoke`: [apple-health.md](apple-health.md#pairing-and-security), audited. `GET /origins` (apps data came from, native or relayed, and the vendors they may relay), `PATCH /origins/{id}` (set or clear the relayed vendor; audited, clears the resolved cache: [origins](apple-health.md#origins-and-relays)), `GET /source-devices`, `PATCH /source-devices/{id}`, `POST /source-devices/{id}/merge`: [source devices](#source-devices) |
| Documents and labs | `POST/GET /documents`, `GET /documents/{id}[/file]`, `DELETE /documents/{id}?derived=keep\|delete`, `GET/POST /documents/{id}/extractions`, `GET /extractors`, `GET /extractions/{id}`, `PATCH /extractions/{id}/rows/{row}`, `POST /extractions/{id}/confirm\|unconfirm`, `GET /lab-results`, `GET /lab-results/{id}/history`, `GET/POST /analytes/aliases`, `DELETE /analytes/aliases/{id}` |
| Exports | `POST /exports`, `GET /exports/{id}`, `GET /exports/{id}/download` |
| Auth and settings | `POST /auth/login\|logout`, `GET /auth/session`, `POST /auth/totp/enroll\|confirm\|disable`, `POST /auth/password`, `GET /auth/sessions`, `DELETE /auth/sessions/{id}`, `GET/POST /api-keys`, `DELETE /api-keys/{id}`, `GET/PATCH /settings` (`withings.notifications`, `documents.external_ai.<provider>.enabled`, `documents.retention_days`, `documents.delete_original_after_confirmation`, `retention.*`: [data-model.md](data-model.md#retention); `sources.priority`, the source order of the [default rule](../resolution-defaults.md#default-rule)), `GET/POST /timezone-periods`, `PATCH/DELETE /timezone-periods/{id}`, `GET/PUT /settings/dashboard` (layout version 1: ordered cards of metric, size `S\|M\|L` and hidden, plus an optional `hero` of up to four metrics (default steps, resting heart rate, nightly HRV, weight) and an optional `dismissed` list of up to 100 alert keys (of 200 characters at most, unique; always in GET), stored as the `dashboard.layout` setting; the curated default with `is_default` until one is saved; audited) |

## Implementing owner endpoints

`make openapi` generates request/response types, parameter binding and the strict-server interface into `internal/api/oapi`, and the TS client into `web/src/lib/api/schema.d.ts`; CI fails on drift, and Spectral (`.spectral.yaml`) lints the spec. An area implements its operations as methods on `*owner` in its own file and registers each generated handler like any other route, `rt.handle("GET /api/v1/system/version", scope(auth.ReadConfig), rt.ops.GetSystemVersion)`, so access stays declared per route and deny-by-default; unregistered operations answer 404. Every operation also needs one line in [`api/authz.yaml`](../../api/authz.yaml), e.g. `GET /api/v1/system/version: {allow: read:config}` (`csrf: true` on unsafe methods; drop `planned: true` when you register a planned one); `go test ./internal/api -run Authz` prints the exact line when it is missing or wrong. Handlers return `problemErr(code, detail)`, `db.ErrNotFound` or `errInvalidCursor` as errors to get the matching problem. Contract tests validate responses with `checkResponse` (`internal/api/contract_test.go`). See `internal/api/owner.go`.

## Source devices

The devices records were measured on (`devices`), not the paired apps of `/devices` ([J20.7](../plan/E20-guided-setup/J20.7-devices.md)).

- `GET /source-devices`: fingerprint, type and model (what the `device_type` and `device_model` selectors match), the owner's `name`, `merged_into`, and `device_types`, the vocabulary a type is set from (`resolve.DeviceTypes`). `include=records` adds active records per connection; measurements are counted from the hourly aggregates like `GET /inventory`.
- `PATCH /source-devices/{id}` `{device_type?, name?}` (session only, audited): a merge patch where null clears. A type set here wins over the normalizer's until cleared; the `devices` trigger clears the resolved cache.
- `POST /source-devices/{id}/merge` `{into}` (session only, audited, irreversible): same provider, and neither device already merged. One transaction marks the moved dates dirty (measurements, and the sleep-derived metrics of sleep sessions; workouts invalidate through their trigger), moves `device_id` in `measurements`, `measurement_groups`, `sleep_sessions`, `workouts` and `health_events` (history included), and points the device and those merged into it at the target. The writer then resolves the fingerprint to the target ([ADR-0016](../adr/0016-canonical-writer.md)); dedupe keys keep the fingerprint, so reprocessing is a no-op. Rules that name the merged device by `device_id` stop matching it.

## Example: resolved day

`GET /api/v1/resolved/daily?start_date=2025-09-14&end_date=2025-09-14&metrics=steps,blood_pressure` on the synthetic fixturegen persona ([fixtures](../../fixtures/README.md)), with the owner's steps rule `maximum_across_sources` over garmin, apple_watch and withings that excludes relayed Apple Health data, and the built-in blood pressure rule. Each metric has the shape of [resolution.md](resolution.md#result-shape); family values are keyed by catalogue code. Abbreviated: `TestResolvedDocExamples` (`internal/api/resolved_integration_test.go`) checks that every field shown is in the real response, and `…` elides the rest of a string.

```json
{"timezone": "Europe/Berlin",
 "days": [{"local_date": "2025-09-14", "metrics": {
   "steps": {"status": "calculated", "value": 6114, "unit": "count",
     "window": {"kind": "local_day", "local_date": "2025-09-14", "start": "2025-09-14T00:00:00+02:00", "end": "2025-09-15T00:00:00+02:00"},
     "rule": {"ref": "rule:steps:2", "version": 2, "strategy": "maximum_across_sources"},
     "inputs": [
       {"group": "garmin", "status": "used", "selected": false, "value": 6061, "basis": "daily_value", "coverage": 1},
       {"group": "apple_watch", "status": "used", "selected": true, "value": 6114, "basis": "intervals", "count": 83, "coverage": 0.628,
        "sources": [{"provider": "apple_health", "connection_id": "conn_…", "device": {"type": "watch"}, "origin": {"key": "com.apple.health.synthetic-watch"}}]},
       {"group": "withings", "status": "no_data", "reason": "no_inputs"},
       {"group": null, "status": "excluded", "reason": "exclude: provider=apple_health relayed=true", "count": 16},
       {"group": null, "status": "not_in_rule", "count": 21}],
     "explanation": "Maximum of 2 sources: garmin 6,061 (daily total); apple_watch 6,114 (83 intervals), selected. No data from withings. Excluded by the rule: apple_health com.garmin.connect.mobile watch (16 rows, exclude: provider=apple_health relayed=true). Outside the rule: apple_health com.apple.health.synthetic-phone phone (21 rows).",
     "links": {"sources": "/api/v1/resolved/steps/2025-09-14/sources"}},
   "blood_pressure": {"status": "direct", "value": {"bp_systolic": 126.5, "bp_diastolic": 75.5, "bp_pulse": 63.5},
     "rule": {"ref": "builtin:blood_pressure:1", "version": 1, "strategy": "first_available"},
     "inputs": [{"group": "bp_monitor", "status": "used", "selected": true, "basis": "mean", "readings": 2},
       {"group": "watch_cuff", "status": "no_data"}, {"group": "manual", "status": "no_data"}],
     "explanation": "Used bp_monitor: bp_systolic 126.5, bp_diastolic 75.5, bp_pulse 63.5 (mean of 2 readings). No data from watch_cuff, manual."}}}]}
```

Inputs list their `record_refs` up to 100 rows; the drilldown links the rest. `/resolved/daily` resolves night metrics (the sleep family, `local_night` rules) on the night of each date and everything else on its `local_day`, from `resolved_cache` where it can. `/resolved/series` pages windows of one metric (`window` = a kind or a bucket size) with the status, the groups and the `providers` behind each point, plus `n` rows and, for intensive metrics, the `min` and `max` sample (bucket series span at most a day for 30 s and 1 min, a week for longer buckets); `/resolved/sleep` lists each night's main episode (`episode` bed and wake times, stage seconds in `result.value`) with every source that recorded it; `/resolved/workouts` lists clusters of overlapping workouts with the member the `heart_rate` rule picks (workouts have no rule family yet, [ADR-0008](../adr/0008-rule-schema.md)). `POST /resolution/preview {spec, start_date, end_date}` validates a draft like rule creation and returns `days[{local_date, draft, active}]`, both resolved live: it stores no rule, cache row or job.

## Example: all-sources drilldown

`GET /api/v1/resolved/steps/2025-09-14/sources` (same data) lists every source, including those excluded or outside the rule. Window keys are dates or UTC window starts; `?window=` picks the kind when the key is ambiguous.

```json
{"metric": "steps", "window": {"kind": "local_day", "local_date": "2025-09-14", "key": "2025-09-14"}, "rule": {"ref": "rule:steps:2", "version": 2},
 "sources": [
  {"group": "garmin", "rule_status": "used", "provider": "garmin", "device": {"type": "watch"},
   "values": {"daily_value": 6061, "interval_sum": 6061, "intervals": 62}, "count": 63,
   "records": {"href": "/api/v1/measurements?connection=conn_…"},
   "provenance": {"raw_payload_ids": ["…"], "normalizer": "fixtureload@1", "fetched_at": "2025-09-14T11:40:00Z"}},
  {"group": null, "rule_status": "excluded", "reason": "exclude: provider=apple_health relayed=true", "provider": "apple_health",
   "origin": {"key": "com.garmin.connect.mobile", "relayed": true, "relayed_provider": "garmin"}, "values": {"interval_sum": 6061, "intervals": 16}},
  {"group": "apple_watch", "rule_status": "used", "provider": "apple_health", "values": {"interval_sum": 6114, "intervals": 83}},
  {"group": null, "rule_status": "not_in_rule", "provider": "apple_health",
   "origin": {"key": "com.apple.health.synthetic-phone"}, "device": {"type": "phone"}, "values": {"interval_sum": 4760, "intervals": 21}}]}
```

## Exports

`POST /exports {scope, format: ndjson|csv, include_raw}` starts an async `export` job (scope admin; 409 while one is queued or running; `scope` is reserved and must be empty). It produces a zip with one NDJSON file per table, each line the row as `to_jsonb` renders it (ids, provenance, superseded and deleted history; connections without credentials, clients and webhook subscriptions without token hashes; lab documents as metadata only, without the PDF, filename, document key or raw extractor responses; no users, sessions, keys, jobs or cursors), `measurements.csv` (format csv: active rows with catalogue codes), `blob_content.ndjson` (include_raw: raw payload and workout file content, base64), and `manifest.json` (format and schema version, row counts, sha256 per file). All tables are read in one snapshot, keyset-paged, and streamed into the blob store, so memory stays flat. `GET /exports/{id}` returns the status; once done it carries a download URL with a fresh token (single use, 10 minutes, only its hash stored). Exports are deleted 7 days after they finish. Layout and the table list: `internal/export`.

`vitamux import ndjson [--merge] EXPORT` (zip or directory) loads an export in one transaction, keeping ids, timestamps, raw metadata, batches, normalizer versions and superseded chains, under the target's single owner. It refuses an instance with health data unless `--merge`, which skips what the target already has (dedupe key chains, raw natural keys, registry natural keys) and adds the rest in fresh id ranges; re-running it changes nothing. Lab documents arrive as tombstones with their confirmed results, revisions and owner aliases (as after a delete with `derived=keep`); runs and extracted rows are exported to read only. It needs the same schema version, the owner created first, and serve stopped; it writes about 10,000 rows/s.
