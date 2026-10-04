# Connectors, ingestion, normalization

Terms are defined in the [glossary](overview.md#glossary). Tables are in [data-model.md](data-model.md).

## Execution modes

Every mode ends in the same pipeline: raw store → normalize → canonical writer.

| Mode | Scheduler | Fetcher | Status |
| --- | --- | --- | --- |
| `in_process` | vitamux | Go connector in the worker | MVP: Withings, future official OAuth APIs |
| `push` | The client | The client uploads raw batches | MVP: iOS app, file importers |
| `remote` | vitamux | Sidecar over private HTTP | v0.2.0 ([E17](../plan/E17-sidecar-connectors/README.md)): community and third-party collectors |

## Connector interface

```go
type Descriptor struct {
    Provider     string        // "withings"
    Name         string        // display name
    Version      string
    Official     bool          // false ⇒ UI shows an "unofficial API" warning; new connections start paused
    Remote       bool          // served by a sidecar (core-set; connection mode 'remote'); no streams = unreachable placeholder
    Upstream     *Upstream     // package, version, source URL of the third-party package a sidecar wraps
    AuthKind     AuthKind      // OAuth2 | InteractiveMFA | DevicePairing | None
    Streams      []StreamSpec  // name, default schedule, correction lookback, max backfill, unit size
    RateLimits   []RateLimitSpec
    Capabilities Capabilities  // Incremental, Backfill, Webhooks, ManualSync
}
type Connector interface {
    Describe() Descriptor
    Plan(ctx context.Context, c Conn, req PlanRequest) ([]WorkUnit, error) // incremental|correction|backfill|manual
    Fetch(ctx context.Context, c Conn, cred Credentials, u WorkUnit, out *RawSink) (FetchResult, error) // one page
}
type Authenticator interface { // AuthKind OAuth2 or InteractiveMFA
    Refresh(ctx context.Context, c Conn, cred Credentials) (Credentials, error)
}
type Interactive interface { // the bootstrap: OAuth authorization code, prompts (credentials, MFA), or both
    Begin(ctx context.Context, in AuthInput) (AuthStep, error)                // first step; no provider call
    Continue(ctx context.Context, c Conn, in AuthInput) (Authorized, error) // callback or prompt values → credentials + account id, or Next step
}
type AuthStep struct { RedirectURL string; Prompt *AuthPrompt; Session []byte } // Session: opaque, sealed server-side
type FetchResult struct { NextCursor json.RawMessage; HighWatermark time.Time; Done bool; RetryAfter time.Duration; Credentials *Credentials }
```

Code: [`internal/connectors`](../../internal/connectors). `NewRegistry(connectors...)` validates descriptors; `EnsureSchedules` turns stream defaults into schedules; `Runtime.Handle` is the `sync` job handler. `Conn.HTTP` is the provider's rate-limited client: it waits for the token buckets, refuses calls while the provider is blocked, and turns a 429 into `RateLimitedError`. Only incremental and manual runs advance the stream cursor; correction runs keep their progress in the job checkpoint.

## Typed errors

| Error (class) | Runtime reaction |
| --- | --- |
| `ErrReauthRequired` (`reauth_required`) | Connection `needs_reauth` (its schedules stop), job `dead`, no retries. A refused access token is first refreshed once and the page retried. |
| `RateLimitedError{RetryAfter}` (`rate_limited`) | Set `provider_rate_state.blocked_until`; reschedule at its end without consuming an attempt; not counted as a failure |
| `ErrTransient` (`transient`), any untyped error | Exponential backoff with full jitter (cap 30 min); connection status kept, failure counted |
| `SchemaDriftError{Endpoint, Fingerprint}` (`schema_drift`) | Store the page's raw as `quarantined`, cursor unchanged; stream and connection `degraded: schema_drift`; job `dead`; the next slot tries again; **never substitute other data** |
| `ErrPermanent` (`permanent`) | Job `dead`, connection `error` (its schedules stop until the owner resumes it) |

Each failure sets `connections.last_error_class` and, except rate limits, increments `consecutive_failures`. A successful run resets both, sets `last_success_at`, marks the stream `ok`, and leaves the connection `degraded` only while another stream still is. Owner-set states (`paused`, `disabled`) are never overwritten.

## Runtime responsibilities

The core does these so connectors stay small:

1. **Credentials**: decrypt; refresh single-flight under `SELECT … FOR UPDATE` on the credential row; persist a rotated token before using it (credentials a fetch returns are stored in its page transaction).
2. **Rate limits**: an in-process token bucket per provider, plus a shared `blocked_until` in PostgreSQL that honours `Retry-After` across restarts.
3. **Raw-first**: blob write + fsync → `raw_payloads` insert → `normalize_batch` job (when anything new was stored) → cursor advance, all in one transaction. A crash leaves at most an orphan blob, which the sweeper collects after 24 h.
4. **Cursors**: JSON cursor + `high_watermark` per (connection, stream). A cursor advances only together with its raw rows.
5. **Correction windows**: each stream declares a `lookback`, which is re-fetched on schedule. Same content is a no-op (sha256). Changed content creates a new raw version and canonical supersession.
6. **Backfill**: a range is split into units (stream `UnitSize` by default) with per-unit status, failed-unit retry, cancel, and resume after restart. Each unit is one exclusive, low-priority `backfill_unit` job that plans with `ModeBackfill` over the unit's range; its last page commits together with the unit's `done` mark, so a unit finishes exactly once. A unit fails on a permanent error or its job's last attempt. Backfills never move the stream cursor.
7. **Manual sync**: a high-priority job with a dedupe key (double-clicks enqueue once).
8. **Schema drift**: shape fingerprint = hash of sorted JSON key paths and value kinds. Extra fields → warning; missing or retyped required fields → drift.
9. **Health**: last success, last error class, consecutive failures, stream lag. Connection states: `active | degraded | needs_reauth | paused | error | disabled`.

## OAuth connection flow

Code: `internal/connectors/auth.go` (`Runtime.BeginAuth`, `ContinueAuth`, `CompleteAuth`, `Disconnect`, reusable `StateSigner`), routes in `internal/api/oauth.go`.

1. `POST /api/v1/providers/{provider}/auth/begin` (new account) or `POST /api/v1/connections/{id}/auth/begin` (reauthorize), owner session only. It stores an `oauth_states` row (user, session, provider, connection, 10 min expiry) and answers `{"redirect_url"}` with a `state` = row id + HMAC (`session-signing` key) over the id and a random browser binding, which is also set as cookie `vitamux_oauth` (`HttpOnly; SameSite=Lax; Path=/oauth/`). The session cookie is `SameSite=Strict`, so it does not come back on the provider's redirect; the binding cookie does.
2. `GET /oauth/{provider}/callback` (public; `HEAD` → 204, no side effects) verifies the HMAC with the cookie, then deletes the row in one statement: single use, and refused when expired, for another provider, or after its session ended. Only then does the connector exchange the code.
3. `account_key` = SHA-256 of the provider account id. A new account gets a connection; the same account again (reconnect) reuses its connection (`UNIQUE (user, provider, account_key)`) and gets fresh credentials; a reauthorization must sign in to the connection's own account (otherwise nothing changes). The connection becomes `active`, default schedules are ensured, a first manual sync is queued, and the event is audited.
4. A prompt step answers `{"state", "prompt": {message, fields: [{name, label, kind: text|password|code}]}}` instead of a redirect; the UI posts `{state, values}` to `POST /api/v1/providers/{provider}/auth/continue` (owner session; the binding cookie is also set for `Path=/api/v1/providers/`). Each step consumes its state row and the next step gets a new one, so a state works once; any error ends the flow. A step's `Session` (e.g. a PKCE verifier or a login session) is sealed into `oauth_states.session` (purpose `credentials`, AAD `auth-session:<id>`) and handed back to `Continue`, never to the browser; owner values are passed through, never stored or logged. The last step finishes as in 3, except that a new connection of an unofficial connector starts `paused` with no first sync.
5. The callback redirects to `/connections?connected=<provider>` or `/connections?auth_error=invalid_state|denied|account_mismatch|exchange_failed|unavailable`.
6. Disconnect (`DELETE /api/v1/connections/{id}?data=keep`) deletes the credentials and sets `disabled`; data, cursors and schedules stay, and connecting the same account again revives the connection. `?data=delete` removes the connection with its data instead ([api.md](api.md#owner-endpoints-apiv1)).

## Push ingest contract

`POST /api/ingest/v1/batches`

- Client token scope `ingest:<connection_id>`.
- `Idempotency-Key` header required ([semantics](api.md#conventions)).
- Body gzip allowed, ≤ 10 MiB compressed and ≤ 50 MiB decompressed, and at most 100× the compressed size (+1 MiB); beyond that `413`.
- `connection_id` must be the token's connection (`403` otherwise).

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
- Binary files (FIT, GPX, export zips, ≤ 25 MiB) go to `POST /api/ingest/v1/batches/{id}/blobs` and are referenced by `blob_sha256`. Upload them first, to any existing batch of the same connection (e.g. one holding the inline items); an item whose blob is not uploaded fails with `422`. A blob no batch references within 24 h is swept.
- `GET /api/ingest/v1/batches/{id}` lists the batch's new raw rows with their status. `normalization` is the `normalize_batch` job's state while queued or running, `failed` if it died or a row failed or was quarantined, else `done`.
- `POST /api/ingest/v1/heartbeat` reports client checkpoint, pending failed units, version, and last error class. The summary goes to `clients.metadata.heartbeat`; per stream, the checkpoint, last success and error class go to `sync_cursors` (`cursor`, `high_watermark`, `degraded` + `status_reason`). A heartbeat with an older `sent_at` than the recorded one is ignored.

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
    Normalize(ctx context.Context, raw RawPayload, env Env) (Output, error)
}
// Output: Measurements, Groups, Sleep, Workouts, Devices, Origins, Tombstones, Warnings
```

Code: `internal/normalize` (interface, `Registry`, `Output.Validate`, `RegisterVersions`, and the writer of [ADR-0016](../adr/0016-canonical-writer.md)).

- **Pure and deterministic.** Same bytes, version, and context give identical output. No clock, randomness, or network.
- **Golden tests** per normalizer (synthetic raw → canonical JSON) with `normtest.Golden`: cases are `testdata/<id>/<case>.raw.<ext>`, goldens `<case>.golden.json` record the version. Output that changes without a `Version()` bump fails; after a bump, `UPDATE_GOLDEN=1 make golden` rewrites.
- Normalizers emit source values and units; the writer applies canonical units and keeps the original value and unit when they differ.
- Every row references `normalizer_versions(name, version, git_sha)`.
- **Jobs** (`normalize.Processor`): `normalize_batch` (payload `{batch_id}`) normalizes each stored payload in its own transaction. A panic, a `Normalize` error, invalid output or a missing normalizer marks it `normalize_failed` with `status_detail` (`normalizer_panic`, `normalizer_error`, `invalid_output`, `no_normalizer`) and a warning; the raw stays. `vitamux reprocess --normalizer --stream --since --until [--wait]` queues a `reprocess` job over the newest raw version of each record that the current normalizer version has not attempted yet: unchanged output only takes the new version, changed records are superseded, a second run selects nothing. Errors and warnings must not carry payload values.
- Third-party adapters may submit canonical records alongside raw (tagged `external:<adapter>@<version>`). These are validated against the canonical schema.

## Withings connector (reference pattern)

Verified API facts and the exact mapping: [providers/withings.md](../providers/withings.md).

- Official OAuth2, in-process ([OAuth connection flow](#oauth-connection-flow)). Each self-hoster registers their own Withings developer app; `VITAMUX_WITHINGS_CLIENT_ID` and `VITAMUX_WITHINGS_CLIENT_SECRET_FILE`. Callback: `${VITAMUX_PUBLIC_URL}/oauth/withings/callback`.
- Stream `withings.measures` (MVP):
  - BP systolic/diastolic/pulse, weight, body composition and the other getmeas types of the catalogue;
  - incremental via `getmeas lastupdate` (hourly), a daily 7-day correction by date, backfill in 30-day units, `offset`/`more` paging; 120 requests per minute;
  - raw: one record per measure group (`measuregrp:<grpid>`).
- Normalizer:
  - measure groups → `bp_reading` / `body_composition` groups, other types plain samples; value = `value × 10^unit`;
  - manual-entry attribution (`attrib` 2, 4) → `manual_entry` flag;
  - unknown meastype → warning (raw kept).
- Rotating refresh tokens (3 h access, the old refresh token dies once the new access token is used) use the single-flight refresh.
- Notifications are optional (setting `withings.notifications`): subscribe with `/webhooks/withings/{hook_token}` (`appli` 1, 2, 4). A POST only enqueues a deduplicated window sync. Polling stays on. Details: [providers/withings.md](../providers/withings.md#how-vitamux-uses-notifications).

## Remote sidecar mode

This mode exists for providers whose only usable client is in another language, typically an existing open-source collector. Built in [E17](../plan/E17-sidecar-connectors/README.md). Protocol `vitamux-connector/1` is frozen by [ADR-0017](../adr/0017-sidecar-protocol.md): spec [`api/connector-sidecar.v1.yaml`](../../api/connector-sidecar.v1.yaml), messages [`schemas/connector-sidecar.v1.json`](../../schemas/connector-sidecar.v1.json), [examples](../../schemas/examples/connector-sidecar/), Go wire types in `internal/connectors/remote`.

- Private HTTP with a shared bearer secret; the sidecar is stateless and has no DB access. The core plans; the sidecar fetches one page per call.
- Endpoints: `GET /v1/describe`, `POST /v1/auth/begin|continue|refresh`, `POST /v1/fetch` (NDJSON raw lines, then one `result` or `error` line).
- Errors are problem+json with `code` = a [typed error](#typed-errors) class.
- A conformance kit (`vitamux connector-test --url`) ships with the implementation.

## Third-party collectors

Vitamux reuses existing open-source collectors instead of rewriting them. They run in their own containers, never inside the core.

| Upstream shape | Path | Who schedules |
| --- | --- | --- |
| A library or client that can fetch on demand | Sidecar wrapping it, speaking `vitamux-connector/1` | vitamux |
| A tool with its own scheduler and storage | Push collector: small uploader that sends its raw responses through [push ingest](#push-ingest-contract) | The tool |

Rules for both paths (how to: [sidecars.md](../sidecars.md)):

- Raw responses go to Vitamux verbatim. Normalizers are in Go in the core, so data stays reprocessable.
- Each upstream lives in `sidecars/<name>/` with `UPSTREAM.md` (repo, package, locked version, license, official status). The lockfile pins the exact version and hashes, the base image is pinned by digest, and the image has a 256 MiB limit and no database access.
- A separate container keeps the core MIT. The license gate fails a sidecar without `UPSTREAM.md` or with an unreviewed copyleft license ([license gate](../sidecars.md#license-gate)).
- Unofficial upstreams use `Official=false`, start disabled, and must surface shape changes as `schema_drift`.
- Wrapped upstreams follow their releases automatically ([E17 J17.5](../plan/E17-sidecar-connectors/J17.5-upstream-tracking.md)): Dependabot bumps the locked version daily after a 1-day cooldown, a bump touching only upstream lines merges when every check is green, a failing one stays open as `upstream-break`, a nightly canary builds against the upstream's default branch, and `ghcr.io/<owner>/vitamux-sidecar-<name>:stable` only ever points at a green build ([details](../sidecars.md#automatic-upstream-updates)). `describe` reports `upstream`, which the core records on the connection and audits.

## Adding a connector

Walkthrough with a toy example: [adapters.md](../adapters.md).

1. Create `internal/connectors/<name>/` with the descriptor, `Plan`, `Fetch`, an optional `Authenticator` and `Interactive`, a normalizer, and synthetic golden fixtures.
2. Wire it in: a migration seeding its `providers` row, one argument to `connectors.NewRegistry` in `cmd/vitamux` `serve()`, one to `normalizers()` (`cmd/vitamux/reprocess.go`), and its settings in `internal/config` and [configuration.md](../configuration.md). No other package changes.
3. Required behaviour: raw-first, typed errors, drift reporting, no secret logging, synthetic fixtures only.
4. Unofficial APIs: `Official=false`, disabled by default, documented warnings.
5. In another language or from an existing project: see [third-party collectors](#third-party-collectors). Only the normalizer goes into the core.
