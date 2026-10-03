# API

## Conventions

- REST + JSON, contract-first in `api/openapi.yaml` (OpenAPI 3.1). Server types (`oapi-codegen`) and the TS client are generated; contract tests validate responses.
- `/api/v1` is for the owner and `/api/ingest/v1` for clients and devices. Only additive changes within v1.
- Instants are RFC 3339 **with offset**, half-open `[start, end)`. Dates are `YYYY-MM-DD` (`start_date`/`end_date`, inclusive), in the user's timezone periods.
- Cursor pagination: `limit` (default 500, max 10,000 for measurements) and an opaque HMAC-protected `cursor`. Responses carry `next_cursor` and `has_more`. No offsets.
- Repeatable filters: `metric`, `provider`, `connection`, `device`, `origin`, `kind`. Expansions via `include=provenance|stages|segments|superseded`.
- Errors are `application/problem+json` with `type`, `title`, `status`, `detail`, `code`, `request_id`, and `errors[]`. Codes: `validation_failed`, `not_found`, `conflict`, `rate_limited`, `reauth_required`, `consent_required`, `unsupported_window`, `rule_warning_unacknowledged`. Also `unauthenticated` and `totp_required` (401), `forbidden` (403), `payload_too_large` (413), `internal_error` (500) and `unavailable` (503). The registry is `internal/api/problem.go`.
- Request bodies are capped per route class (`bodyClasses` in `internal/api/middleware.go`): 1 MiB owner JSON, 10 MiB ingest batches, 25 MiB uploads. `X-Forwarded-For`/`-Proto` are trusted only from `VITAMUX_TRUSTED_PROXIES` (comma-separated CIDRs; empty trusts none).
- `Idempotency-Key` is required on ingest POSTs and accepted on job-creating owner POSTs.
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
| `POST /devices/pair`, `GET /devices/self`, `POST /devices/self/rotate-token` | Device pairing and config ([apple-health.md](apple-health.md)) |

## Owner endpoints (`/api/v1`)

| Area | Endpoints |
| --- | --- |
| Catalogue | `GET /metrics`, `GET /metrics/{code}` |
| Source data | `GET /measurements`, `/groups?kind=`, `/blood-pressure`, `/sleep[/{id}]`, `/workouts[/{id}]`, `/provenance/{entity}/{id}` |
| Resolved | `GET /resolved/daily`, `/resolved/series?metric&start&end&window`, `/resolved/sleep`, `/resolved/workouts`, `/resolved/{metric}/{window_key}/sources`; `POST /resolution/preview` (draft rule × range; no writes) |
| Rules and overrides | `GET /rules`, `GET /rules/{metric}/versions`, `POST /rules/{metric}/versions`, `POST /rules/{metric}/activate`, `GET/POST /overrides`, `POST /overrides/{id}/revoke` |
| Coverage and health | `GET /coverage?start_date&end_date&metric`, `GET /system/status`, `GET /jobs` |
| Connections | `GET/POST /connections`, `GET/PATCH/DELETE /connections/{id}` (`?data=keep\|delete`), `POST …/auth/begin\|continue`, `POST …/sync`, `POST …/backfills`, `GET …/runs`, `GET …/streams`, `POST …/streams/{s}/reset-cursor` |
| Manual data | `POST /measurements/manual` (provider `manual`, audited) |
| Devices | `POST /devices/pairing-codes`, `GET /devices`, `POST /devices/{id}/request-anchor-reset`, `POST /devices/{id}/revoke` |
| Documents and labs | `POST/GET /documents`, `GET /documents/{id}[/file]`, `DELETE /documents/{id}?derived=keep\|delete`, `POST /documents/{id}/extractions`, `GET /extractions/{id}`, `PATCH /extractions/{id}/rows/{row}`, `POST /extractions/{id}/confirm`, `GET /lab-results`, `GET /lab-results/{id}/history`, `GET/POST /analytes/aliases` |
| Exports | `POST /exports`, `GET /exports/{id}`, `GET /exports/{id}/download` |
| Auth and settings | `POST /auth/login\|logout`, `GET /auth/session`, `POST /auth/totp/enroll\|confirm\|disable`, `GET/POST /api-keys`, `DELETE /api-keys/{id}`, `GET/PATCH /settings`, `GET/POST /timezone-periods` |

## Example: resolved day

`GET /api/v1/resolved/daily?start_date=2026-09-14&end_date=2026-09-14&metrics=steps,blood_pressure` (synthetic). Each metric has the shape described in [resolution.md](resolution.md#result-shape).

```json
{"timezone": "Europe/Amsterdam",
 "days": [{"local_date": "2026-09-14", "metrics": {
   "steps": {"status": "calculated", "value": 11342, "unit": "count",
     "window": {"kind": "local_day", "start": "2026-09-14T00:00:00+02:00", "end": "2026-09-15T00:00:00+02:00"},
     "rule": {"ref": "user:steps", "version": 4, "strategy": "maximum_across_sources"},
     "inputs": [
       {"group": "garmin", "status": "used", "selected": true, "value": 11342, "basis": "daily_value", "coverage": 0.96},
       {"group": "apple_watch", "status": "used", "selected": false, "value": 10877, "basis": "intervals", "coverage": 0.92},
       {"group": "withings", "status": "no_data"}],
     "explanation": "Maximum of 2 eligible sources: Garmin daily total 11,342 (selected); Apple Watch 10,877. Withings had no data.",
     "links": {"sources": "/api/v1/resolved/steps/2026-09-14/sources"}},
   "blood_pressure": {"status": "direct", "value": {"systolic": 121, "diastolic": 79, "pulse": 64},
     "rule": {"ref": "builtin:blood_pressure", "version": 1, "strategy": "latest"},
     "inputs": [{"group": "withings", "status": "used", "selected": true, "basis": "reading", "readings": 2}],
     "explanation": "Latest of 2 Withings readings (components from the same reading)."}}}]}
```

## Example: all-sources drilldown

`GET /api/v1/resolved/steps/2026-09-14/sources` lists every source, including those excluded or outside the rule:

```json
{"metric": "steps", "window": {"kind": "local_day", "local_date": "2026-09-14"}, "rule": {"ref": "user:steps", "version": 4},
 "sources": [
  {"group": "garmin", "rule_status": "used", "provider": "garmin", "device": {"type": "watch"},
   "values": {"daily_value": 11342, "interval_sum": 11290, "intervals": 96},
   "records": {"href": "/api/v1/measurements?metric=steps&connection=conn_…&start=…&end=…&include=provenance"},
   "provenance": {"raw_payload_ids": ["4411"], "normalizer": "garmin.daily_summary@3", "fetched_at": "2026-09-14T21:02:11Z"}},
  {"group": null, "rule_status": "excluded", "reason": "exclude: relayed=true", "provider": "apple_health",
   "origin": {"key": "com.garmin.connect.mobile", "relayed_provider": "garmin"}, "values": {"interval_sum": 11288}},
  {"group": null, "rule_status": "not_in_rule", "provider": "apple_health",
   "origin": {"name": "iPhone"}, "device": {"type": "phone"}, "values": {"interval_sum": 6034}}]}
```

## Exports

`POST /exports {scope, format: ndjson|csv, include_raw}` starts an async job. It produces a zip with one file per table (rows with provenance; connections without credentials) and a manifest with schema versions and sha256 checksums. The download uses a one-time short-lived token. `vitamux import ndjson` round-trips the export.
