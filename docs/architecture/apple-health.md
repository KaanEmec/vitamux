# Apple Health bridge

This document is for epic E15. [E22](../plan/E22-ios-app/README.md) moved the app into the Vitamux iOS app and adds every Apple Watch type ([ios-app › Apple Watch](ios-app.md#apple-watch)). Platform facts were checked in [J15.1](../plan/E15-apple-health/J15.1-platform-contract.md) on 2026-10-04 against Apple's HealthKit documentation and the Xcode 27 SDK headers (cited as [Apple docs][apple-docs] below). What only a phone can answer is listed in the [device checklist](../apple-health-device-checklist.md).

[apple-docs]: https://developer.apple.com/documentation/healthkit

## Why a device component

HealthKit data lives encrypted on the iPhone (and Watch). It is readable only through the HealthKit framework, by an app holding the HealthKit entitlement and per-type user authorization. Apple offers no server API. The backend therefore receives uploads from a companion app, or a manual export as a fallback.

## Structure

| Part | Contents |
| --- | --- |
| `apple/HealthBridgeKit` (Swift package, reusable) | **Core**: payload models, batching, gzip, retry/backoff, idempotency keys, anchor store, Keychain token store. **HealthKit**: type registry, per-type authorization, `HKAnchoredObjectQuery` loop, `HKObserverQuery` + `enableBackgroundDelivery`, deletions, mapping, and the Watch detail readers (ECG voltages, heartbeat series, route locations and their workout link, activity summaries). `HealthStore` sits behind a protocol so it can be faked in tests; a store that does not implement a detail read fails that type instead of sending it without its detail. |
| `apple/VitamuxApp` (`Features/AppleHealth`) | The iOS app embeds the kit ([ios-app](ios-app.md#apple-health)): one-tap, QR or manual pairing, metric-group picker, per-type status, Sync now, anchor reset, privacy text. Owns the capabilities and purpose strings. It replaced the Bridge app (`apple/HealthBridgeApp`, removed in J22.14). |
| Backend | Pairing and device-token endpoints, the generic batch endpoint ([connectors.md](connectors.md#push-ingest-contract)), and the Go `healthkit.samples` normalizer. No other iOS coupling in the core. |

## Permissions

- `NSHealthShareUsageDescription` (read only; no write scope). Capabilities: HealthKit, plus its nested Background Delivery, which adds `com.apple.developer.healthkit.background-delivery` (iOS 15+). Without that entitlement `enableBackgroundDelivery` fails with `errorAuthorizationDenied` ([entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.healthkit.background-delivery), [enableBackgroundDelivery](https://developer.apple.com/documentation/healthkit/hkhealthstore/enablebackgrounddelivery(for:frequency:withcompletion:)), [Xcode setup](https://developer.apple.com/documentation/xcode/configuring-healthkit-access)).
- Per-type authorization is requested on demand when the user enables a metric group (Heart, Activity, Body, Sleep, Workouts, Vitals, Nutrition). Type registry v1: `Registry.v1` in `HealthBridgeHealthKit`; an identifier the phone's iOS does not know is skipped. `Registry.v2` ([ADR-0024](../adr/0024-watch-data.md#type-registry-v2)) is v1 plus the Apple Watch types, so v1 anchors survive the upgrade, and six groups that stay off until turned on (`MetricGroup.isOptIn`): ECG, beats, routes (`requires` workouts), cycle, symptoms and mind. Authorization asks for one group's types only, including `HKSeriesType.heartbeat()`, `workoutRoute()`, the ECG type and `activitySummaryType()`. The kit sends the identifiers HealthKit reports: State of Mind is `HKDataTypeStateOfMind`.
- Read denial is not observable: a denied type simply looks empty, and `authorizationStatus(for:)` reports only the write permission ([authorizing access](https://developer.apple.com/documentation/healthkit/authorizing-access-to-health-data), [authorizationStatus](https://developer.apple.com/documentation/healthkit/hkhealthstore/authorizationstatus(for:))). The app shows "requested", not "granted". The server flags a type that has been silent for 7 days as "possibly denied".
- Two different history limits. `earliestPermittedSampleDate()` (iOS 9+) is a **system-wide** floor that applies to every app, not the user's choice; queries before it return samples after it ([doc](https://developer.apple.com/documentation/healthkit/hkhealthstore/earliestpermittedsampledate())). On iOS 27+ the user can also grant a limited window of recent data per type, discoverable only through `getEarliestAuthorizedSampleDate(for:)`. It omits a type that is denied or fully granted, so limited access is the only state the app can see ([doc](https://developer.apple.com/documentation/healthkit/hkhealthstore/getearliestauthorizedsampledate(for:completion:))). Backfill starts at max(system floor, per-type authorized date when reported, configured start); data before an authorized date is unknown, not absent. The boundary is compared with a sample's end date.
- Enabling a type gives it a new anchor and a full pull. Disabling stops queries and keeps server data. The server can request an anchor reset per type; re-sent data is deduplicated by UUID.

## Sync algorithm

1. For each enabled type, run `HKAnchoredObjectQuery(type, anchor, limit)`.
2. Anchored queries return added samples and `HKDeletedObject`s together. Deleted objects are temporary and the system may drop them at any time ([doc](https://developer.apple.com/documentation/healthkit/hkdeletedobject)), so a long-offline device can miss a deletion; a full pull after an anchor reset repairs it. Build a batch from samples and `deletedObjects`: ≤ 2,000 samples or 1 MiB gzip. Idempotency key = `sha256(device|type|anchor_before|first_uuid|last_uuid|count)`.
3. Upload. **Persist the new anchor only after `202`.** On failure, retry with backoff; the unchanged anchor means the same batch is re-sent and treated as a duplicate. A `409` (same key, body re-read later with a new `fetched_at`) also counts as accepted. `external_key` = `<type>:<idempotency key>`.
4. Loop until a page returns fewer than `limit` results. The limit is per type (`HealthType.pageLimit`): ECG 5, heartbeat series 100, route 1, else 2,000. Before upload each ECG, series or route gets its detail read; a failed read fails the page, so the anchor stays. Routes and effort scores carry `workout_uuid` when HealthKit links them, whatever order they arrive in.
   Activity summaries have no anchor: the first sync reads from the backfill start (not before 2015) in 366-day pages, later syncs re-read the last 7 days. The stored anchor is the last day read, so a backfill resumes; the page hash covers its summaries, so only a changed day makes a new batch.
5. Triggers: Sync now, app launch, observer callbacks with background delivery (always call the completion handler), and optionally `BGAppRefreshTask`. A type whose background delivery iOS refuses keeps its observer and does not stop the others (`observe` returns the refusals).
6. iOS controls timing. Promise eventual sync only, and test on a **physical device**.

Background-delivery facts ([HKObserverQuery](https://developer.apple.com/documentation/healthkit/hkobserverquery), [Executing observer queries](https://developer.apple.com/documentation/healthkit/executing-observer-queries), [HKObserverQueryCompletionHandler](https://developer.apple.com/documentation/healthkit/hkobserverquerycompletionhandler)):

- An observer query fires on saves **and deletions** but carries no detail. Follow it with an anchored query.
- Register every observer query in `application(_:didFinishLaunchingWithOptions:)`, so it exists when HealthKit launches the app, and pair each with `enableBackgroundDelivery` for the same type.
- Call the completion handler when done. If the app fails to respond three times, HealthKit backs off and then stops delivering.
- HealthKit wakes the app at most once per chosen frequency. Some types are capped at hourly, enforced silently; on iOS `stepCount` is the documented example. Apple's documented "immediate" exceptions are watchOS-only (heart-rate and audio-exposure events, VO2 max, handwashing and others), so on iPhone assume hourly for quantities.
- Background delivery does not run in the Simulator.
- Not documented, so tested on a device: delivery while the phone is locked and its data store is unavailable, and delivery latency per type (J15.7).

## Payload

Stream `healthkit.samples.v1`, one item body per type page ([schema](../../schemas/healthkit-samples.v1.json)). Correlation members go in `objects`, workouts in `workout` (`activity_type`, `duration_s`, `totals`). Apple Watch types add optional fields (`workout_uuid`, `ecg`, `beats`, `route`, `state_of_mind`, `activity_summary`, `workout.stats`, `workout.events`, `workout.activities`) and smaller pages ([ADR-0024](../adr/0024-watch-data.md#payload-fields), [synthetic examples](../../schemas/examples/healthkit-samples.v1/)):

```json
{"type": "HKQuantityTypeIdentifierHeartRate",
 "anchor": {"before_hash": "b3c1…", "after_hash": "7a90…", "query_started_at": "2026-09-14T08:00:03+02:00"},
 "samples": [{"uuid": "6F0D…", "start": "2026-09-14T07:31:05.120+02:00", "end": "2026-09-14T07:31:05.120+02:00",
   "value": 61, "unit": "count/min",
   "source_revision": {"bundle_id": "com.apple.health.5C1…", "name": "Apple Watch", "version": "11.0", "product_type": "Watch7,1", "os_version": "11.0.0"},
   "device": {"name": "Apple Watch", "manufacturer": "Apple Inc.", "model": "Watch", "hardware_version": "Watch7,1", "software_version": "11.0"},
   "metadata": {"HKMetadataKeyHeartRateMotionContext": 1, "HKTimeZone": "Europe/Amsterdam"},
   "was_user_entered": false}],
 "deleted": [{"uuid": "0A1B…"}]}
```

Mapping:

- The BP correlation becomes one item → a `bp_reading` group. A correlation holds systolic and diastolic quantity samples (and food correlations hold dietary samples). It has no read permission of its own: the app requests its members, and a correlation read returns only the members the user allowed ([HKCorrelation](https://developer.apple.com/documentation/healthkit/hkcorrelation), [HKCorrelationQuery](https://developer.apple.com/documentation/healthkit/hkcorrelationquery)). The member samples also exist on their own, so the registry syncs only the correlation to avoid double counting. Anchored behaviour for correlations, including a deleted correlation versus its members, is not documented and is a device-checklist item.
- Sleep analysis values (raw values from the SDK header, `HKCategoryValues.h`): `inBed` 0, `asleepUnspecified` 1, `awake` 2, `asleepCore` 3, `asleepDeep` 4, `asleepREM` 5. The old `asleep` is the same raw value 1 (deprecated in iOS 16), and the stage values need iOS 16. `asleepCore` covers AASM stages N1 and N2 ([doc](https://developer.apple.com/documentation/healthkit/hkcategoryvaluesleepanalysis)). Mapping: `asleepCore` → light, `asleepDeep` → deep, `asleepREM` → rem, `asleepUnspecified` and a legacy `asleep` → unstaged sleep, `inBed` and `awake` → `sleep_sessions` with stages. Several sources may write overlapping samples. Store the `HKTimeZone` (`HKMetadataKeyTimeZone`) Apple recommends for sleep samples.
- Workouts map to `workouts`, their events and activities to `workout_segments`. Routes become `workout_route` events with a route blob, linked by `workout_uuid`.
- ECG becomes an `ecg_recording` event with a waveform blob, a heartbeat series becomes `rr_interval` samples, activity summaries become daily values, and State of Mind and the cycle, symptom and mind categories go to `health_events` ([ADR-0024](../adr/0024-watch-data.md#storage-no-new-tables)).
- `deleted` → tombstones.
- `was_user_entered` → `manual_entry` flag.

Normalizer (`internal/connectors/applehealth`, [J15.2](../plan/E15-apple-health/J15.2-backend-pairing-normalizer.md)):

- Every record is keyed by its type and UUID, so a deleted UUID tombstones exactly that record. Untyped BP members (v1) are told apart by value: the higher pressure is systolic.
- Sleep samples group per origin into sessions; a gap over 1 h starts a new one. A session is keyed by its earliest sample, so deleting that sample deletes the session. A night split across two uploads becomes two sessions.
- Category events go to `health_events` with a level word ([metrics.md](../metrics.md#events)); `AppleStandHour` becomes one `stand_hours` interval per hour (1 stood, 0 idle).
- The local date uses the `HKTimeZone` metadata key (`HKMetadataKeyTimeZone`), else the owner's timezone periods. Without that key the timestamp offset is only the phone's zone at upload time.
- A type without a mapping, or a sample in an unexpected unit, is a warning; the raw page stays for a later normalizer version.
- Apple Watch types (version 4, [J22.17](../plan/E22-ios-app/J22.17-watch-normalizer.md)): a heartbeat series becomes `rr_interval` samples keyed `<uuid>#<beat index>` (a series deletion withdraws them all); ECG and route documents (`vitamux.waveform/1`, `vitamux.route/1`) are written to the blob store from raw, content-addressed, and referenced by their event; a route links to its workout by `context.workout_uuid`, else by time from the same origin, when it is read (`GET /workouts/{id}/route`), so arrival order does not matter. A single-activity workout stores no activity segment (HealthKit reports it as one activity). Enum values map to words; an unknown value keeps its number (`unknown_<n>`) with a warning.

## Origins and relays

- `provider = apple_health` is the transport. `data_origins.origin_key` = bundle id; `devices` come from `HKDevice`.
- Apple-native origins (`com.apple.health.*`, Apple devices) have `is_native`. Origins matching `known_relay_origins` get `relayed_provider` (e.g., Garmin Connect → `garmin`). The UI (Settings › Devices) lists every origin it has seen so the owner can classify unknown ones: `PATCH /origins/{id}` writes `data_origins.relayed_provider_id` (an owner edit wins; `known_relay_origins` only seeds new origins), and the `relayed` selector and the all-sources view read it live. Rows already stored keep the `relayed` quality flag they were written with until they are reprocessed.
- The [source filter](#source-filter) decides per device which origin apps' data the phone takes at all, so a directly connected provider's copy in Health is not collected; the `relayed` exclusion in rules stays for anything that still arrives.
- Example rules: Apple Watch HR first (`provider: apple_health, device_type: watch, relayed: false`), a scale app's weight first (`origin_key: …`), or exclude relayed Garmin data (`relayed: true`).

### Source filter

Per paired device, take or ignore per origin app and optionally per type ([J22.25](../plan/E22-ios-app/J22.25-apple-health-source-filter.md); `internal/sourcefilter`, HealthBridgeKit's `SourceFilter`).

- **Storage.** `clients.source_filter` holds only the owner's explicit choices (`{"origins": [{"bundle_id", "name", "mode": take|ignore|per_type, "types"}]}`; `per_type` takes only the listed types) with `source_filter_version`, bumped on every change. Defaults are evaluated on every read, so connecting, disabling or deleting a direct connection changes them and never an explicit choice.
- **Decision**, for an app's bundle id: an explicit choice for it, else for its parent app (a Watch extension's id minus `.watchkitapp`, `.watchkitextension` and similar; HealthKit reports no parent); else the default: Apple's own sources (`com.apple.*`) take; an origin relaying a provider with a connection that is not disabled ignores, with the reason shown ("You get WHOOP directly; its copy in Apple Health would count twice"); everything else, unknown apps included, takes. Relaying is the seen origin's classification (`data_origins.relayed_provider_id`, owner edits included), else a `known_relay_origins` pattern.
- **API.** `GET /api/ingest/v1/devices/self` returns `source_filter` (`version`, the explicit `origins`, the `default_ignore` patterns). The phone reports the apps it found per enabled type (`HKSourceQuery`, with each type's newest sample time, never values) with `PUT /api/ingest/v1/devices/self/sources`. Both clients read `GET /api/v1/devices/{id}/source-filter` (one row per app reported, chosen or seen, with mode, default and reason, classification, what it writes and records held raw) and replace the explicit list with `PUT` (optional `version`, 409 when stale), audited as `device.source_filter`. An app whose data goes from ignored to taken gets an anchor reset for the types it now takes (`*` when not reported), and the device's raw payloads holding its ignored records are queued for normalization again.
- **Phone.** Each anchored query of a sample type excludes the ignored sources with `NOT HKQuery.predicateForObjects(from:)`, so ignored data never leaves the phone and an app that appears between discovery and the query is taken. When a source the last read excluded is present and taken now, the type's anchor is reset first, so its history is pulled; the filter and each type's excluded set are kept with the anchors.
- **Server guard.** Rows that still arrive from an ignored origin (an old app build, a race with a change) are kept raw and not normalized: `normalize.DropOrigins` leaves them out, the payload gets an `ignored_by_filter` warning and `ignored_records` counts them per origin and item. Explore (`GET /inventory?include_ignored=true`) and the all-sources view (`GET /sources/series?include_ignored=true`) list them only on request. Normalizing the payload again after a take writes them.
- **Screens.** Apple Health › Sources in the app (`vitamux://apple-health/sources`) and Settings › Devices › Sources per paired iPhone in the panel: one row per app with what it writes, the classification (native, relayed, direct), Take or Ignore, "Choose types…", the default's reason, and a link to Explore filtered by that origin.

### Brand origin sets

Used by the [suggested defaults](resolution-defaults.md#how-brands-are-selected), which reference a brand's set instead of a hard-coded id. Checked 2026-10-04. **Bundle id** = the app's id in the App Store lookup API; HealthKit reports the iPhone app's bundle id as the source, but this has not yet been observed on a device for any brand (J15.7 records it). **Seeded** = in `known_relay_origins` ([00003](../../internal/db/migrations/00003_sources.sql), [00025](../../internal/db/migrations/00026_relay_origins.sql)). What an app writes comes from vendor support pages where one exists; the others are secondary and marked.

| Brand | Bundle id | Writes to Health (reported) | Seeded |
| --- | --- | --- | --- |
| Oura | `com.ouraring.oura` | Heart rate (1-min), active energy, respiratory rate, sleep with stages, mindful minutes, height. **No HRV, no resting HR** ([Oura](https://support.ouraring.com/hc/en-us/articles/360025438734), [summary](https://www.sensai.fit/blog/wearable-apple-health-integration-what-syncs-oura-whoop-garmin)) | yes |
| WHOOP | `com.whoop.iphone` | Heart rate during workouts and sleep only, resting HR, SpO2, respiratory rate, active energy, workouts, sleep as asleep or awake without stages. **HRV is not exported** (SDNN vs RMSSD) ([Terra](https://tryterra.co/blog/whoop-syncs-health-data-to-apple-health-ee298d328f41), secondary) | yes |
| Garmin Connect | `com.garmin.connect.mobile` | Heart rate, steps, distance, flights, active and resting energy, sleep, weight, body fat, BMI, workouts; one-way. **No HRV** ([Garmin forum](https://forums.garmin.com/apps-software/mobile-apps-web/f/garmin-connect-mobile-ios/109531/health-app-not-syncing-correctly/621541), secondary) | yes |
| Google Health (Fitbit) | `com.fitbit.FitbitMobile` | Since version 5.05 (August 2026): exercise, sleep, steps, distance, heart rate, vitals. HRV does not transfer ([MacRumors](https://macrumors.com/2026/08/03/fitbit-apple-health-syncing), [TechRepublic](https://techrepublic.com/article/news-fitbit-apple-health-sync)) | yes |
| Polar Flow | `fi.polar.polarflow` | Workouts, heart rate **in workouts only**, active and resting energy, steps, sleep (duration, onset, wake), weight ([Polar](https://support.polar.com/en/support/connecting_polar_flow_with_apple_health)) | yes |
| Withings (Health Mate) | `com.withings.wiScaleNG` | Weight and body composition; heart rate only from the app's own measurement, not continuous heart rate or ECG ([Withings community](https://support.withings.com/hc/en-us/community/posts/360014261138-Heath-Mate-not-syncing-all-items-with-Apple-Health)) | yes |
| Ultrahuman | `com.ultrahuman.ios` | Unconfirmed. An aggregator lists sleep stages, heart rate, resting HR, HRV (SDNN), SpO2 ([Sahha](https://sahha.ai/integrations/ultrahuman/), secondary) | no, unverified |
| Eight Sleep | `com.eightsleep.Eight` | Unconfirmed; no vendor page found | no, unverified |
| Strava | `com.strava.stravaride` | Unconfirmed; no vendor page found | no, unverified |
| Zepp (Amazfit) | `com.huami.watch`; Zepp Life: `HM.wristband` | Unconfirmed. Steps, sleep, weight reported; Zepp-only scores (PAI, stress) do not cross ([Mi support](https://www.mi.com/global/support/article/KA-12890/), secondary) | no, unverified |

Rules:

- No brand writes nightly RMSSD to Health; that needs a direct connector ([resolution-defaults](resolution-defaults.md#how-brands-are-selected)).
- Unseeded origins stay `relayed_provider_id = NULL` until the owner classifies them in the UI. Seed an unverified brand only after a device shows its source bundle id. Seeded ids are provisional until J15.7 confirms them on a device.
- Watch-side extensions of a brand app may report a different bundle id; match on what the device shows, not on the table.

## Pairing and security

- The UI creates a pairing code (`POST /api/v1/devices/pairing-codes`): 8 Crockford base32 symbols shown as `XXXX-XXXX` (case, spaces and dashes ignored), single use, 10-minute TTL, at most 5 per 10 minutes, stored hashed. The QR (`qr_payload`) is the JSON `{"url", "code"}` with the public URL (`VITAMUX_PUBLIC_URL`) and nothing else. Without a public URL the panel gets 503, while an app session (one-tap pairing, [ios-app](ios-app.md#apple-health)) gets the code without `url` and `qr_payload`.
- The app exchanges it at `POST /api/ingest/v1/devices/pair` (`{"code", "name"}` → `201 {"device_id", "connection_id", "token"}`) for a 256-bit device token (`vmx_cli_…`, a client of kind `device`). Every device of the owner joins one `apple_health` push connection, created at the first pairing. Wrong codes are throttled per address like failed logins (429). The server stores the token hashed; the device keeps it in the Keychain (`AfterFirstUnlockThisDeviceOnly`, so background uploads work while locked) and can replace it with `POST /devices/self/rotate-token`.
- Anchor resets: the owner asks with `POST /api/v1/devices/{id}/request-anchor-reset` (`{"types": […]}`, empty = `*`, every type). `GET /api/ingest/v1/devices/self` returns each type's latest request time; the device applies those newer than the last it applied, so a lost response loses nothing.
- HTTPS only. Revoking a device (or rotating its token) takes effect immediately: the old token's next request is 401. Certificate pinning is deferred.

## Export importer (fallback)

`vitamux import apple-health-export export.zip|export.xml` (`internal/imports`; CLI only, no UI upload yet):

- Streams `export.xml` (only that zip entry) with limits on its size (8 GiB), compression ratio (100:1), zip entries, element count and depth, attributes and children per record. The inline DTD is ignored and only XML's predefined entities resolve. `make fuzz` covers the parser (`FuzzParseExport`).
- Raw first: every `Record`, `Correlation` (with its member records) and `Workout` (with its `WorkoutStatistics`) is stored as found, attributes and `MetadataEntry` values included, in stream `apple_health.export.v1`: pages of 1,000 records of one type, cut between nights for sleep. Workout events and routes, HRV beat lists, `ActivitySummary` and clinical records are not read.
- The `apple_health.export` normalizer converts each record to the [payload](#payload) sample shape (export units such as lb, km, mi or °F are converted to the registry units; category case names to raw values) and reuses the HealthKit mapping, so the registry v2 records an export holds map like the app's: the new quantities, the mind, cycle-tracking and symptom categories (the case names of their enums), and a workout's heart-rate statistics as average and maximum (version 2, J22.17). Exports keep ECGs as CSV files, routes as GPX files and activity summaries as `ActivitySummary` elements, which are not read: an export summary has no time zone, so its day could not share the app summary's row without the two replacing each other on every import. Origins are `export:<sourceName>`, since exports carry no bundle id, so the owner classifies them; the bundle-id groups of the built-in rules do not match them.
- Exports have no UUIDs: a record's id is a hash of its natural key `(type, start, end, value, sourceName, device)`, with instants compared and the device's object address dropped. A page's key is its type and the hash of its record ids, so importing the same export again stores and changes nothing.
- Rows go to the owner's `apple_health` connection (the one devices pair to, created if missing). **Overlap:** a record that matches a row the app already synced (same type, origin name, start and end within the export's whole second, and value) is kept raw in the page's `overlapping` list, reported per type with its date range, and not normalized: device-type rule groups would otherwise count it twice. A record imported before the app synced it is withdrawn (tombstoned) when the export is imported again. The report is printed and stored in `import_runs.stats`.

It is a one-off backfill, not equivalent to sync: import after the app's first full sync, and again after a later export.

## Distribution

The app is built from source with the owner's Apple developer team. Decision (J15.1, Q2 in [project.md](project.md#open-questions)): a **paid Apple Developer Program membership is the supported path** for a long-lived install. A free account can build and run it, but only as a contributor's development build. No App Store or TestFlight in the first release.

- The HealthKit capability is available to the free tier ([capabilities table](https://developer.apple.com/help/account/reference/supported-capabilities-ios/)).
- A free account's provisioning profile expires after 7 days and allows 3 apps per device ([membership comparison](https://developer.apple.com/support/compare-memberships/)). The app would stop syncing weekly until reinstalled, which defeats background sync.
- The nested Background Delivery capability is not listed separately in Apple's table, so whether a free profile carries `com.apple.developer.healthkit.background-delivery` is **unconfirmed**. J15.7 tries it on a device. Without a paid account the [export importer](#export-importer-fallback) is the fallback.
