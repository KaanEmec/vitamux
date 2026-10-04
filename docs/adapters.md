# Writing a connector

How to add a provider as an in-process Go connector. The contract and the runtime's duties are in [connectors.md](architecture/connectors.md); this page is the walkthrough. A complete toy connector built from this page alone lives in [`internal/connectors/example`](../internal/connectors/example) (registered only in its own tests). The production reference is [`internal/connectors/withings`](../internal/connectors/withings).

Not every source is an in-process connector:

| Source | Path |
| --- | --- |
| HTTP API Vitamux can call on a schedule (OAuth, API key, none) | This guide |
| A device or app that holds the data (phone, desktop exporter) | The client uploads raw batches: [push ingest](architecture/connectors.md#push-ingest-contract); you write only the normalizer (steps 6–7) |
| A client library in another language, or an existing collector | [Third-party collectors](architecture/connectors.md#third-party-collectors) |

## What you write, what the runtime does

You write a package `internal/connectors/<name>/` with a `Connector` (describe, plan, fetch), optionally an `Authenticator` and `Interactive` for OAuth, one `normalize.Normalizer` per stream, and tests. The runtime (`connectors.Runtime`) does the rest: schedules, jobs and retries, credential storage and single-flight refresh, rate limits and `Retry-After`, the raw-first commit with the cursor advance, backfill units, health, and the typed-error state machine. A connector never touches the database, the blob store or the job queue, and never logs payloads or credentials.

## 1. Names

| Name | Rule | Example |
| --- | --- | --- |
| Provider code | `^[a-z][a-z0-9_]*$`; the `providers.code` row | `example` |
| Stream | `^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`, conventionally `<provider>.<feed>`; also the normalizer id | `example.heart_rate` |
| External key | Stable per upstream record across fetches, pages and correction runs; same record ⇒ same key | `sample:<upstream id>` |

The external key is what makes refetching idempotent: same key and same bytes is a no-op, same key and different bytes is a new raw version that supersedes the old one's canonical rows.

## 2. Descriptor

`Describe()` returns a `connectors.Descriptor`, validated by `connectors.NewRegistry`:

```go
func (*Connector) Describe() connectors.Descriptor {
	return connectors.Descriptor{
		Provider: "example", Version: "1",
		Official: true,                     // false: the UI warns about an unofficial API
		AuthKind: connectors.AuthNone,      // AuthOAuth2 and AuthInteractiveMFA also need an Authenticator
		Streams: []connectors.StreamSpec{{
			Name:     "example.heart_rate",
			Interval: time.Hour,              // incremental schedule; >= 1 minute, 0 = unscheduled
			Lookback: 0,                      // > 0 adds a correction schedule re-fetching that window
		}},
		RateLimits:   []connectors.RateLimitSpec{{Requests: 60, Per: time.Minute}}, // all apply, per provider
		Capabilities: connectors.Capabilities{Incremental: true, ManualSync: true},
	}
}
```

Validation: a version and at least one stream are required; `Capabilities.Incremental` requires every stream to have an `Interval`; `Capabilities.Backfill` requires `0 < UnitSize <= MaxBackfill`; `CorrectionEvery` (default 24 h) is 0 or at least a minute. A test that calls `connectors.NewRegistry(New(Config{}))` catches all of this.

## 3. Plan

`Plan(ctx, conn, req)` splits one sync into `[]connectors.WorkUnit` without calling the provider. `req.Mode` is one of:

| Mode | When | Input | Cursor |
| --- | --- | --- | --- |
| `ModeIncremental` | Schedule | `req.Cursor` (stored stream cursor, nil before the first sync), `req.HighWatermark` | Advanced by every committed page |
| `ModeManual` | Owner's "sync now", first sync after connecting | Same as incremental | Advanced |
| `ModeCorrection` | Correction schedule (`Lookback`), webhook hints | `req.From`, `req.To` | Untouched; progress lives in the job checkpoint |
| `ModeBackfill` | Owner-started backfill, one unit of `UnitSize` at a time | `req.From`, `req.To` | Untouched |

Return an error wrapping `connectors.ErrPermanent` for a mode you do not support or an unreadable cursor. Usually one unit is enough: the runtime pages through it. Between pages the runtime replaces the unit's `Cursor` with the previous page's `NextCursor`.

## 4. Fetch

`Fetch(ctx, conn, cred, unit, out)` fetches **one page** and returns a `FetchResult`:

1. Call the provider only through `conn.HTTP.Do(req)`: it waits for the rate-limit buckets, refuses calls while the provider is blocked, and turns HTTP 429 into `*connectors.RateLimitedError` (honouring `Retry-After`). Every other status comes back as a response, so check `res.StatusCode` yourself. Read the body through a limit of your own (e.g. `io.ReadAll(io.LimitReader(res.Body, 8<<20+1))`; the client's own cap is 16 MiB, after which the read fails with `httpx.ErrResponseTooLarge`). A body over your limit is a failed request, not drift: return `ErrPermanent`. Read one byte past the limit to notice it, because a silently cut body would fail to decode and look like drift.
2. Map failures to [typed errors](architecture/connectors.md#typed-errors): wrap `connectors.ErrReauthRequired` (credentials refused, usually 401; the runtime refreshes once and retries before giving up; only with `AuthOAuth2` and `AuthInteractiveMFA`: an `AuthNone` connector has nothing to refresh, so its 401 and 403 are `ErrPermanent`), `connectors.ErrTransient` (5xx, network, a body read that failed midway), `connectors.ErrPermanent` (other 4xx, a 2xx other than the one you expect, an unexpected 3xx, an oversized body: a request that can never succeed), or return `&connectors.RateLimitedError{RetryAfter: d}` for a quota signalled in the body. Any other error counts as transient. Errors must not contain payload values or credentials. The client follows redirects itself (at most five, never from https to http), so a 3xx that reaches you is a response you cannot use: permanent.
3. Decode only the fields **the connector itself uses**: the envelope (page token, server time) and, per record, the external key and the source time for the watermark. Check that they are present and of the right type. If not, the provider changed shape: put the page into `out` as one raw item (so it is kept, quarantined) with its own `ExternalKey`, e.g. `page:<request params>` (the same request again supersedes it), and return `&connectors.SchemaDriftError{Endpoint: "...", Fingerprint: fp}` with `fp, _ := ingest.ShapeFingerprint(body)`. A 200 whose body is truncated or not the JSON you expect is drift, not transient: a retry would read the same thing. Never fall back to other data. Unknown extra fields are fine. What a record's content means (values, units, a missing optional field) is the normalizer's business, and it warns (step 6); the connector does not judge it.
4. Put one `ingest.RawItem` per upstream record with `out.Put`: `ExternalKey` (step 1; an item without one fails the commit), `ContentType` (`application/json`), `Body` (the record's bytes as received, not re-encoded from a struct), and `Request: ingest.Request{Endpoint: "GET /v1/...", Params: ...}`. The runtime sanitizes `Request` before storing it; still never put tokens there. `Stream` and `FetchedAt` default to the unit's stream and the page start.
5. Return the result:
   - `NextCursor`: the cursor for the next page, or for the stream once the unit is done (incremental). nil keeps the current one.
   - `Done`: the unit is complete. A page that is not done must have a `NextCursor` (otherwise `ErrPermanent`).
   - `HighWatermark`: the newest source time on the page (zero if unknown); shown as stream lag.
   - `RetryAfter`: optional pause after this page (soft quota).

The page's raw items, the normalize job and the cursor advance commit in one transaction, so a crash repeats at most one page. Design the cursor so that repeating a page or a whole unit is harmless.

**Cursor design.** Every committed page stores its `NextCursor` as the stream's cursor, so the next `Plan` can receive a cursor from the middle of a run (after a crash, or when a later page failed). `Plan` and `Fetch` must resume from both shapes. Keep three parts in one JSON object: what the stream resumes from (e.g. `since`, sent as `updated_since`), the page token (only while paging) and the server time of the run's first page (only while paging). The first page's server time travels inside the cursor from page to page and becomes the new `since` only on the last page, so records changed during paging are fetched again next time rather than missed. See `cursor` in [`example.go`](../internal/connectors/example/example.go).

## 5. Authentication

| `AuthKind` | Credentials | You implement |
| --- | --- | --- |
| `AuthNone` | None; `cred` is empty | Nothing |
| `AuthOAuth2` | Access and refresh token, sealed by the runtime | `Authenticator.Refresh` and `Interactive.Begin`/`Continue` |
| `AuthInteractiveMFA` | Provider session in `Credentials.Extra` | Same interfaces; reserved for unofficial adapters |
| `AuthDevicePairing` | Push clients only | Not an in-process connector |

`Refresh` returns new `Credentials` and maps a refused refresh token to `ErrReauthRequired`. `Begin` builds the provider's consent URL from `AuthInput.RedirectURL` (`${VITAMUX_PUBLIC_URL}/oauth/<provider>/callback`) and `AuthInput.State`, must not call the provider, and returns `connectors.ErrAuthUnavailable` when the client id and secret are not configured. `Continue` exchanges the callback's code and returns the credentials plus the provider account id (only its SHA-256 is stored). The runtime handles state signing, the callback route, connection creation, reconnects and audit ([OAuth flow](architecture/connectors.md#oauth-connection-flow)). Copy the shape of `internal/connectors/withings/auth.go`.

## 6. Normalizer

One `normalize.Normalizer` per stream turns one stored raw record into canonical rows ([contract](architecture/connectors.md#normalizer-contract)):

```go
type Normalizer struct{}

func (Normalizer) ID() string                    { return "example.heart_rate" } // normally the stream name
func (Normalizer) Version() int                  { return 1 }                    // bump on any output change
func (Normalizer) Accepts(stream, _ string) bool { return stream == "example.heart_rate" }
func (Normalizer) Normalize(ctx context.Context, raw normalize.RawPayload, env normalize.Env) (normalize.Output, error)
```

- **Pure**: only `raw.Body` (and `raw.Stream`, `raw.ExternalKey`, `env.Provider`). No clock, randomness, network or database. Take a record's identity (`Key.ExternalID`) from the body, never from `raw.ExternalKey`: the golden harness sets that to the case name (step 7), so a key derived from it would differ from production.
- **Metric codes** come from the catalogue: [metrics.md](metrics.md) lists codes, allowed kinds, units and groups. Grouped metrics (blood pressure, body composition) go into `Output.Groups`, never as plain measurements. A code that does not exist yet is added in `internal/catalog` first ([metric-catalog.md](architecture/metric-catalog.md#rules)).
- **Values in the source unit**: set `Unit` to what the provider sends (it must be a unit the catalogue converts); the writer converts and keeps the original.
- **Kinds**: `catalog.Sample` has `Start` only; `Interval` and `DailyValue` have `End` too.
- **Keys**: `Key{RecordType, ExternalID, Component}` is the record's stable upstream identity (dedupe and supersession). Use the provider's id; set `Component` when one record yields several rows.
- **Devices and origins**: list each in `Output.Devices` (`Fingerprint` stable per provider, `Type` such as `watch`, `scale`, `bp_monitor`) and reference it by fingerprint from the rows, so rules can select by device. A record without a device leaves `Device` empty and lists none.
- **Time zones**: leave `Zone` empty unless the record carries its own offset; local dates come from the owner's timezone periods.
- **Unknown or skipped input** becomes an `Output.Warnings` entry (a code and a detail without values), not an error. Return an error only when the record cannot be read at all; the raw row is kept as `normalize_failed`.

`Output.Validate()` checks codes, kinds, units, groups and references; the writer and the golden harness call it.

## 7. Golden tests

Put synthetic raw cases in `testdata/<normalizer id>/<case>.raw.json`, each a JSON object whose top level has `"synthetic": true` (the [fixture policy](architecture/project.md#synthetic-fixtures-policy); `make fixture-guard` checks it), and test with the harness. The marker means a golden body is never byte-identical to what the provider sends, so the normalizer must ignore the field (no strict decoding, no warning for unknown keys). `Stream` and `Provider` below are constants you declare in the package (`const Provider = "example"`, `const Stream = "example.heart_rate"`, used by `Describe()` and `Accepts` too):

```go
func TestGoldenHeartRate(t *testing.T) {
	normtest.Golden(t, Normalizer{}, normalize.RawPayload{Stream: Stream, ContentType: "application/json"},
		normalize.Env{Provider: Provider})
}
```

The first run fails because the goldens are missing: `UPDATE_GOLDEN=1 go test ./internal/connectors/<name>/` writes `<case>.golden.json`; review them and commit both. Later output changes fail until you bump `Version()`. Cover the normal case, an unknown or skipped record (warning) and an unreadable one (pinned error). The golden records the error text, so return a fixed message: `encoding/json` syntax errors can echo payload bytes, which also makes them a leak in production logs. A case that is not JSON at all is `<case>.raw.txt` with a `synthetic: true` line in its first five lines (the guard accepts that marker in text files). Build bigger datasets with [`tools/fixturegen`](../tools/fixturegen) when the provider is part of the synthetic persona.

### Field ledgers

Every stream also keeps `testdata/<normalizer id>/fields.json`, `{"synthetic": true, "fields": {...}}`, mapping each raw JSON path of its cases (`.` for keys, `[]` for array elements, e.g. `response.records[].cycle.day_strain`) to a catalogue code (several comma separated) or to `raw: <reason>`. The harness fails on a path the ledger does not name, so a new provider field is catalogued or explained before it ships. Only identifiers, baselines, UI text and values derivable from stored rows stay raw ([policy](architecture/metric-catalog.md#rules)); a field whose code exists but is not mapped yet says so (`raw: not mapped yet: J25.4`).

## 8. Tests for the connector

- **Unit** (offline): the descriptor validates (`connectors.NewRegistry`), `Plan` for each mode, and any decoder on its own (valid page, each drift case).
- **Integration** (`//go:build integration`, real PostgreSQL through [`internal/db/dbtest`](development.md#writing-integration-tests)): `Fetch` needs the runtime's `HTTPClient`, so it is tested through `connectors.Runtime`. Serve the provider with [`internal/testutil/fakeprovider`](../internal/testutil/fakeprovider) (scripted requests and replies, including 429, 5xx and malformed bodies). [`example_integration_test.go`](../internal/connectors/example/example_integration_test.go) is the complete pattern; the steps:
  1. `dbURL, pool := dbtest.Migrated(t)`; insert your `providers` row through `dbtest.Pool(t, dbURL, db.OwnerRole)` (the app role cannot write `providers`; production gets it from a migration, step 9). The linter bans `pgx` imports outside `internal/db` (depguard), so you cannot name `*pgxpool.Pool` in a helper signature: wrap the pool in closures (`mustExec := func(sql string, args ...any) { ... pool.Exec(ctx, sql, args...) ... }`, and one for counting rows) and pass `db.New(pool)` to the runtime.
  2. Insert the rows the runtime needs (columns in the [schema reference](schema/README.md)): a `users` row (`password_hash` is NOT NULL; any synthetic string), a `timezone_periods` row whose `valid_from` is before every sample you serve (local dates come from it), and a `connections` row with `provider_id` (select it from `providers` by `code`), `mode = 'in_process'` and `status = 'active'`.
  3. Build `connectors.New(connectors.Config{DB, Blobs, Keys, Registry, Log})` with a temporary master key (`crypto.WriteKeyFile`, `crypto.Load`) and blob store (`blob.Open`).
  4. Enqueue a `jobs.KindSync` job (`jobs.Enqueue` with `jobs.SyncPayload{Stream, Mode: connectors.ModeManual, Slot}` and the connection id) and process it with a `jobs.Runner`: `runner.Register(jobs.KindSync, rt.Handle)` and `runner.Register(ingest.KindNormalizeBatch, (&normalize.Processor{DB, Blobs, Registry, Log}).BatchJob())`. There is no "run until idle" helper: start `runner.Run(ctx)` in a goroutine with a short `jobs.Config.Poll` (e.g. 50 ms), stop it in `t.Cleanup`, and poll the `jobs` table until no row is `running` or `queued` and due (with a deadline). A page's normalize job is enqueued in the same transaction as the page, so one idle check covers both.
  5. Assert on `raw_payloads`, `measurements` and `sync_cursors`, and that the logs contain nothing sensitive. An `AuthNone` connector has no secret to look for, so assert that no payload value (a device id) and no page token appears. Give both `connectors.Config.Log` and `jobs.Config.Log` (and the `Processor`) one logger writing to a mutex-guarded buffer: the runner's goroutines write to it while the test reads, and a bare `bytes.Buffer` is a data race.

## 9. Wire it into the binary

1. **Provider row**: a new migration in `internal/db/migrations/` with `INSERT INTO providers (code, name) VALUES ('<code>', '<Name>');` (and a `-- +goose Down` that deletes it).
2. **Registry**: add the connector to `connectors.NewRegistry(...)` in `serve()` of [`cmd/vitamux/main.go`](../cmd/vitamux/main.go). That is the only list of connectors.
3. **Normalizer**: add it to `normalizers()` in [`cmd/vitamux/reprocess.go`](../cmd/vitamux/reprocess.go), used by `serve` and `vitamux reprocess`.
4. **Settings**: client ids and secrets go into [`internal/config`](../internal/config/config.go) (secrets through `secret(...)`, so only `_FILE` works in production) and a row each in [configuration.md](configuration.md) (a test enforces it), plus the Compose files when they belong in the default stack.
5. **Docs**: `docs/providers/<name>.md` with the verified API facts (endpoints, paging, limits, error codes, how Vitamux syncs), and a row in [docs/README.md](README.md).
6. **Fuzzing**: a `Fuzz*` target for each decoder of provider responses, listed in `FUZZ_TARGETS` in the Makefile.
7. **Unofficial APIs**: `Official: false`, documented warnings in the provider doc, and every unexpected shape a `SchemaDriftError`. (Starting such connections disabled is a rule of [connectors.md](architecture/connectors.md#adding-a-connector) that nothing enforces yet.)

Known gap: connections are created by the OAuth flow (`POST /api/v1/providers/{provider}/auth/begin`) or as push connections. A connector with `AuthNone` or an API key has no way to create its connection through the API or UI yet; it needs a small create path in `internal/api` and the connect wizard.

## Checklist

- [ ] Raw first: every record is `Put` verbatim before anything is interpreted; drift keeps the page.
- [ ] Typed errors for every failure; nothing untyped where the class is known.
- [ ] Repeating a page, a unit or a whole sync changes nothing.
- [ ] No secrets or payload values in errors, warnings, logs or `Request`.
- [ ] Normalizer is pure, versioned and golden-tested; fixtures are synthetic.
- [ ] Descriptor test, Plan test, integration test through the runtime.
- [ ] Provider row, registry line, normalizer line, configuration rows, provider doc.
