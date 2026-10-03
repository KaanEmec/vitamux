# Connectors, ingestion, normalization

Terms are defined in the [glossary](overview.md#glossary). Tables are in [data-model.md](data-model.md).

## Execution modes

Every mode ends in the same pipeline: raw store → normalize → canonical writer.

| Mode | Scheduler | Fetcher | Status |
| --- | --- | --- | --- |
| `in_process` | vitamux | Go connector in the worker | MVP: Withings, future official OAuth APIs |
| `push` | The client | The client uploads raw batches | MVP: iOS app, file importers |
| `remote` | vitamux | Sidecar over private HTTP | Designed; built with the first sidecar connector ([migration.md](migration.md)) |

## Connector interface

```go
type Descriptor struct {
    Provider     string        // "withings"
    Version      string
    Official     bool          // false ⇒ UI shows an "unofficial API" warning
    AuthKind     AuthKind      // OAuth2 | InteractiveMFA | DevicePairing | None
    Streams      []StreamSpec  // name, default schedule, correction lookback, max backfill, unit size
    RateLimits   []RateLimitSpec
    Capabilities Capabilities  // Incremental, Backfill, Webhooks, ManualSync
}
type Connector interface {
    Describe() Descriptor
    Plan(ctx context.Context, c Conn, req PlanRequest) ([]WorkUnit, error) // incremental|correction|backfill|manual
    Fetch(ctx context.Context, c Conn, cred Credentials, u WorkUnit, out RawSink) (FetchResult, error)
}
type Authenticator interface { // only if AuthKind needs it
    Begin(ctx context.Context, c Conn, in AuthInput) (AuthStep, error)                         // redirect URL or challenge
    Continue(ctx context.Context, c Conn, st AuthState, in AuthInput) (AuthStep, *Credentials, error)
    Refresh(ctx context.Context, c Conn, cred Credentials) (Credentials, error)
}
type FetchResult struct { NextCursor json.RawMessage; Done bool; RetryAfter time.Duration }
```

## Typed errors

| Error | Runtime reaction |
| --- | --- |
| `ErrReauthRequired` | Connection `needs_reauth`, schedules paused, no retries |
| `ErrRateLimited{RetryAfter}` | Set `provider_rate_state.blocked_until`; reschedule without consuming an attempt |
| `ErrTransient` | Exponential backoff with full jitter (cap 30 min) |
| `ErrSchemaDrift{Endpoint, Fingerprint}` | Store raw as `quarantined`; stream `degraded: schema_drift`; **never substitute other data** |
| `ErrPermanent` | Job `dead`, shown in UI |

## Runtime responsibilities

The core does these so connectors stay small:

1. **Credentials**: decrypt; refresh single-flight under `SELECT … FOR UPDATE` on the credential row; persist a rotated token before using it.
2. **Rate limits**: an in-process token bucket per provider, plus a shared `blocked_until` in PostgreSQL that honours `Retry-After` across restarts.
3. **Raw-first**: blob write + fsync → `raw_payloads` insert → cursor advance, all in one transaction. A crash leaves at most an orphan blob, which the sweeper collects after 24 h.
4. **Cursors**: JSON cursor + `high_watermark` per (connection, stream). A cursor advances only together with its raw rows.
5. **Correction windows**: each stream declares a `lookback`, which is re-fetched on schedule. Same content is a no-op (sha256). Changed content creates a new raw version and canonical supersession.
6. **Backfill**: a range is split into units with per-unit status, failed-unit retry, and resume after restart.
7. **Manual sync**: a high-priority job with a dedupe key (double-clicks enqueue once).
8. **Schema drift**: shape fingerprint = hash of sorted JSON key paths and value kinds. Extra fields → warning; missing or retyped required fields → drift.
9. **Health**: last success, last error class, consecutive failures, stream lag. Connection states: `active | degraded | needs_reauth | paused | error | disabled`.

## Push ingest contract

`POST /api/ingest/v1/batches`

- Client token scope `ingest:<connection_id>`.
- `Idempotency-Key` header required.
- Body gzip allowed, ≤ 10 MiB compressed and ≤ 50 MiB decompressed.

```json
{
  "schema": "vitamux.ingest.batch/1",
  "connection_id": "conn_…",
  "client": {"kind": "device", "name": "healthbridge-ios", "version": "0.2.0"},
  "items": [{
    "stream": "healthkit.samples.v1", "external_key": "HKQuantityTypeIdentifierHeartRate:anchor:b3c1",
    "fetched_at": "2026-09-14T09:02:11Z", "content_type": "application/json", "sha256": "9f2c…",
    "request": {"endpoint": "optional", "params": {}},
    "body": {"…": "verbatim source payload"}
  }],
  "provenance": {"migration_source": null}
}
```

`202` → `{"batch_id": "bat_…", "items": [{"external_key": "…", "status": "stored|duplicate|new_version", "raw_payload_id": "88123"}], "normalization": "queued"}`

- Raw data is durable before `202`. Normalization runs as a job afterwards.
- Binary files (FIT, GPX, export zips) go to `POST /api/ingest/v1/batches/{id}/blobs` and are referenced by `blob_sha256`.
- `POST /api/ingest/v1/heartbeat` reports client checkpoint, pending failed units, version, and last error class.

### Schemas and versioning

- Contract: [`schemas/ingest-batch.v1.json`](../../schemas/ingest-batch.v1.json), [`schemas/heartbeat.v1.json`](../../schemas/heartbeat.v1.json) (JSON Schema 2020-12), with [examples](../../schemas/examples/) and the ingest paths in [`api/openapi.yaml`](../../api/openapi.yaml). Go types and validation: `internal/ingest` (a test keeps them in agreement with the schema files).
- v1 is **frozen** (Gate G3, 2026-10-03). Within v1, changes are additive and optional only: new optional fields, new `client.kind` or error class values. Removing, renaming, retyping or tightening anything needs `…/2` in `schema`, a new schema file, and a period in which the server accepts both.
- Envelope objects reject unknown fields; `body`, `request.params` and `checkpoint` are free-form.
- `connection_id` is `conn_` + the UUID as 32 lowercase hex characters.
- `sha256` is over the exact bytes of `body` as sent; the server stores those bytes verbatim.
- The `body` of each stream is versioned by the stream name (`healthkit.samples.v1`); its normalizer owns that format.

## Normalizer contract

```go
type Normalizer interface {
    ID() string      // "withings.measures", "healthkit.samples"
    Version() int    // bump on ANY output-affecting change
    Accepts(stream, shapeFingerprint string) bool
    Normalize(ctx context.Context, raw RawPayload, nc NormalizeContext) (Output, error)
}
// Output: Measurements, Groups, Sleep, Workouts, Devices, Origins, Tombstones, Warnings
```

- **Pure and deterministic.** Same bytes, version, and context give identical output. No clock, randomness, or network.
- **Golden tests** per normalizer (synthetic raw → canonical JSON). CI fails when output changes without a `Version()` bump.
- Canonical units are applied. The original value and unit are kept when they differ.
- Every row references `normalizer_versions(name, version, git_sha)`.
- Third-party adapters may submit canonical records alongside raw (tagged `external:<adapter>@<version>`). These are validated against the canonical schema.

## Withings connector (reference pattern)

- Official OAuth2, in-process. Each self-hoster registers their own Withings developer app; client id and secret come from secret files. Callback: `${VITAMUX_PUBLIC_URL}/oauth/withings/callback` (GET exchange; HEAD → 204).
- Stream `withings.measures` (MVP):
  - BP systolic/diastolic/pulse, weight, and body composition;
  - incremental via `getmeas lastupdate`; backfill in date chunks with paging; default hourly poll.
- Normalizer:
  - measure groups → `bp_reading` / `body_composition` groups; value = `value × 10^unit`;
  - manual-entry attribution → `manual_entry` flag;
  - unknown meastype → warning (raw kept).
- Rotating refresh tokens use the single-flight refresh.
- Notifications are optional: subscribe with `/webhooks/withings/{hook_token}`. A POST only enqueues a deduplicated window sync. Polling stays on.
- Withings facts are assumptions until J08.1 verifies them against current docs ([project.md](project.md#assumptions-to-verify)).

## Remote sidecar mode (deferred)

This mode exists for providers whose only usable client is in another language. Sketch of protocol `vitamux-connector/1`:

- Private HTTP with a shared bearer secret; the sidecar is stateless and has no DB access.
- Endpoints: `GET /v1/describe`, `POST /v1/auth/begin|continue|refresh`, `POST /v1/fetch` (NDJSON raw lines, then a `result` line with `next_cursor`, `done`, `retry_after_s`, and any rotated credentials).
- Errors are problem+json with `code ∈ {reauth_required, rate_limited, transient, schema_drift, permanent}`.
- A conformance kit (`vitamux connector-test --url`) ships with the implementation.

## Adding a connector

1. Create `internal/connectors/<name>/` with the descriptor, `Plan`, `Fetch`, an optional `Authenticator`, a normalizer, and golden fixtures from `tools/fixturegen`.
2. Register it with one line in the registry. No other package changes.
3. Required behaviour: raw-first, typed errors, drift reporting, no secret logging, synthetic fixtures only.
4. Unofficial APIs: `Official=false`, disabled by default, documented warnings.
