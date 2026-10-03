# Apple Health bridge

This document is for epic E15. Facts marked "verify" are confirmed in J15.1.

## Why a device component

HealthKit data lives encrypted on the iPhone (and Watch). It is readable only through the HealthKit framework, by an app holding the HealthKit entitlement and per-type user authorization. Apple offers no server API. The backend therefore receives uploads from a companion app, or a manual export as a fallback.

## Structure

| Part | Contents |
| --- | --- |
| `apple/HealthBridgeKit` (Swift package, reusable) | **Core**: payload models, batching, gzip, retry/backoff, idempotency keys, anchor store, Keychain token store. **HealthKit**: type registry, per-type authorization, `HKAnchoredObjectQuery` loop, `HKObserverQuery` + `enableBackgroundDelivery`, deletions, mapping. `HealthStore` sits behind a protocol so it can be faked in tests. |
| `apple/HealthBridgeApp` (minimal SwiftUI) | Pairing (QR or manual), metric-group picker, per-type status, Sync now, anchor reset, privacy text. Owns the capabilities and purpose strings. |
| Backend | Pairing and device-token endpoints, the generic batch endpoint ([connectors.md](connectors.md#push-ingest-contract)), and the Go `healthkit.samples` normalizer. No other iOS coupling in the core. |

## Permissions

- `NSHealthShareUsageDescription` (read only; no write scope). Capabilities: HealthKit and HealthKit background delivery (verify the entitlement).
- Per-type authorization is requested on demand when the user enables a metric group (Heart, Activity, Body, Sleep, Workouts, Vitals).
- Read denial is not observable. The app shows "requested", not "granted". The server flags a type that has been silent for 7 days as "possibly denied".
- Limited history: report `earliestPermittedSampleDate()` (verify). Backfill starts at max(that date, configured start).
- Enabling a type gives it a new anchor and a full pull. Disabling stops queries and keeps server data. The server can request an anchor reset per type; re-sent data is deduplicated by UUID.

## Sync algorithm

1. For each enabled type, run `HKAnchoredObjectQuery(type, anchor, limit)`.
2. Build a batch from samples and `deletedObjects`: ≤ 2,000 samples or 1 MiB gzip. Idempotency key = `sha256(device|type|anchor_before|first_uuid|last_uuid|count)`.
3. Upload. **Persist the new anchor only after `202`.** On failure, retry with backoff; the unchanged anchor means the same batch is re-sent and treated as a duplicate.
4. Loop until a page returns fewer than `limit` results.
5. Triggers: Sync now, app launch, observer callbacks with background delivery (always call the completion handler), and optionally `BGAppRefreshTask`.
6. iOS controls timing and some types are hourly at most. Promise eventual sync only, and test on a **physical device**.

## Payload

Stream `healthkit.samples.v1`, one item body per type page:

```json
{"type": "HKQuantityTypeIdentifierHeartRate",
 "anchor": {"before_hash": "b3c1…", "after_hash": "7a90…", "query_started_at": "2026-09-14T08:00:03+02:00"},
 "samples": [{"uuid": "6F0D…", "start": "2026-09-14T07:31:05.120+02:00", "end": "2026-09-14T07:31:05.120+02:00",
   "value": 61, "unit": "count/min",
   "source_revision": {"bundle_id": "com.apple.health.5C1…", "name": "Apple Watch", "version": "11.0", "product_type": "Watch7,1", "os_version": "11.0.0"},
   "device": {"name": "Apple Watch", "manufacturer": "Apple Inc.", "model": "Watch", "hardware_version": "Watch7,1", "software_version": "11.0"},
   "metadata": {"HKMetadataKeyHeartRateMotionContext": 1, "HKMetadataKeyTimeZone": "Europe/Amsterdam"},
   "was_user_entered": false}],
 "deleted": [{"uuid": "0A1B…"}]}
```

Mapping:

- The BP correlation becomes one item → a `bp_reading` group.
- Sleep analysis: `asleepCore` → light, `asleepDeep` → deep, `asleepREM` → rem, plus `asleepUnspecified`, `inBed`, `awake` → `sleep_sessions` with stages.
- Workouts map to `workouts`. Routes are deferred.
- `deleted` → tombstones.
- `was_user_entered` → `manual_entry` flag.

## Origins and relays

- `provider = apple_health` is the transport. `data_origins.origin_key` = bundle id; `devices` come from `HKDevice`.
- Apple-native origins (`com.apple.health.*`, Apple devices) have `is_native`. Origins matching `known_relay_origins` get `relayed_provider` (e.g., Garmin Connect → `garmin`). The UI lists every origin it has seen so the user can classify unknown ones.
- Example rules: Apple Watch HR first (`provider: apple_health, device_type: watch, relayed: false`), a scale app's weight first (`origin_key: …`), or exclude relayed Garmin data (`relayed: true`).

## Pairing and security

- The UI creates a pairing code: single use, 10-minute TTL, rate-limited. The QR holds only the public URL and the code.
- The app exchanges it at `POST /api/ingest/v1/devices/pair` for a 256-bit device token. The server stores it hashed; the device keeps it in the Keychain (`AfterFirstUnlockThisDeviceOnly`, so background uploads work while locked).
- HTTPS only. Revoking a device takes effect immediately. Certificate pinning is deferred.

## Export importer (fallback)

`vitamux import apple-health-export` and a UI upload:

- stream-parse `export.xml` with zip-bomb and size limits;
- dedupe on the natural key `(type, start, end, value, sourceName, device)`, since exports have no UUIDs;
- report overlap with app-synced data instead of merging blindly.

This is documented as a one-off backfill, not equivalent to sync.

## Distribution

The app is built from source with the owner's Apple developer team. A paid membership is likely needed for long-lived installs and background delivery (verify; open question Q2 in [project.md](project.md#open-questions)). No App Store or TestFlight in the first release.
