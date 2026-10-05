# ADR-0024 Apple Watch data contract: registry v2, optional payload fields, existing tables

Status: Proposed (accepted once the owner reviews it) · Date: 2026-10-05 · Deciders: owner

## Context
[E22](../plan/E22-ios-app/README.md) brings in everything Apple Watch records. There is no watchOS app: the Watch writes into the iPhone's Health store, and the app reads it through HealthBridgeKit under the frozen `healthkit.samples.v1` contract ([ADR-0014](0014-healthkit-contract.md)). The kit ([J22.16](../plan/E22-ios-app/J22.16-watch-kit.md)) and the normalizer ([J22.17](../plan/E22-ios-app/J22.17-watch-normalizer.md)) are built in parallel, so the types, bytes and storage are fixed here first ([J22.15](../plan/E22-ios-app/J22.15-watch-data-contract.md)). Background: [ios-app › Apple Watch](../architecture/ios-app.md#apple-watch), [apple-health](../architecture/apple-health.md).

Platform facts were checked on 2026-10-05 against the iOS 27.0 SDK headers in Xcode (`HealthKit.framework`) and [Apple's HealthKit documentation](https://developer.apple.com/documentation/healthkit):

- `Registry.v1` already holds most Watch quantities (running and cycling metrics, mobility, audio, AFib burden, heart-rate recovery, falls, wrist temperature, breathing disturbances). Watch types it lacks: the ones in the table below.
- Every new type except the activity summary is an `HKSampleType`. It is read with `HKAnchoredObjectQuery`, and deletions arrive as `HKDeletedObject`, as in v1. Three need a second query per sample for their detail: `HKElectrocardiogramQuery` (voltages), `HKHeartbeatSeriesQuery` (beat times and gap flags) and `HKWorkoutRouteQuery` (`CLLocation` batches). A route's workout is found with `predicateForObjectsFromWorkout:`, and an effort score's workout with `HKWorkoutEffortRelationshipQuery` (iOS 18).
- `HKActivitySummaryType` is an `HKObjectType` and not a sample type. A summary has no UUID, source, anchor or deletion, and no observer query or background delivery. It is read by date with `HKActivitySummaryQuery`.
- Workout events (`workoutEvents`) and activities (`workoutActivities`, iOS 16) are properties of `HKWorkout`, so they arrive with the workout.
- No HealthKit type exposes Apple's retrospective ovulation estimates. The wrist temperature behind them is already in v1.
- `electrocardiogramType()` and `activitySummaryType()` have no identifier constant in the SDK. The kit sends the identifier HealthKit reports at run time. Unknowns go to the [device checklist](../apple-health-device-checklist.md) (D13 to D18).

## Decision

### Type registry v2
`Registry.v2` = `Registry.v1` + the rows below. v1 types keep their group and anchor. A new type starts with a full pull. The phone's seven v1 groups keep their v1 defaults, and the six new groups are **off** until the owner turns each on. HK ids omit their `HKQuantityTypeIdentifier`, `HKCategoryTypeIdentifier` or `HKDataTypeIdentifier` prefix.

| Group | Default | Adds | iOS | Stored as |
| --- | --- | --- | --- | --- |
| heart (v1) | on | `IrregularHeartRhythmEvent` | 12.2 | event `irregular_rhythm_alert` |
| | | `HeartRateVariabilityRMSSD` (ms) | 27.0 | `hrv_rmssd` |
| activity (v1) | on | `CrossCountrySkiingSpeed` (m/s) | 18.0 | `speed_xc_ski` |
| | | activity summary (`HKActivitySummaryQuery`) | 9.3 | daily values (`D`) of `active_energy`, `move_time`, `exercise_time`, `stand_hours` |
| workouts (v1) | on | `WorkoutEffortScore`, `EstimatedWorkoutEffortScore` (`appleEffortScore`) | 18.0 | `apple_workout_effort`, `apple_workout_effort_estimated` |
| | | workout events, activities and statistics (payload only, no new type) | 10.0 / 16.0 | `workout_segments`, `workouts.avg_hr_bpm` and `max_hr_bpm` |
| vitals (v1) | on | `HandwashingEvent` | 14.0 | event `handwashing` |
| mind | off | `MindfulSession`, State of Mind (`HKStateOfMind`) | 10.0, 18.0 | events `mindful_session`, `state_of_mind` |
| cycle | off | the 18 cycle-tracking categories (`MenstrualFlow` … `MenopausalState`) | 9.0 to 27.0 | events, [metric-catalog › Events](../architecture/metric-catalog.md#events) |
| symptoms | off | the 39 symptom categories | 13.6, 14.0 | events `symptom_<name>` |
| ecg | off | electrocardiogram (`HKElectrocardiogram`) | 14.0 | event `ecg_recording` + waveform blob |
| beats | off | `HeartbeatSeries` | 13.0 | `rr_interval` samples |
| routes | off, needs workouts | `HKWorkoutRouteTypeIdentifier` | 11.0 | event `workout_route` + route blob |

**Page sizes** (anchored query limit): ECG 5, heartbeat series 100, route 1, everything else 2,000 as in v1. Activity summaries: today and the 6 days before on every sync. On the first sync they are read from the backfill start, 366 days per page. The Batcher still cuts chunks at 1 MiB gzip and sends a single larger sample alone, within the server's 10 MiB limit ([connectors](../architecture/connectors.md#push-ingest-contract)). A 30 s ECG has about 15,000 voltages, and a 12 h route at 1 Hz has 43,200 points, which stays well under that limit.

### Payload fields
Still stream `healthkit.samples.v1`. These sample fields are all optional ([schema](../../schemas/healthkit-samples.v1.json), [synthetic examples](../../schemas/examples/healthkit-samples.v1/)):

| Field | On | Content |
| --- | --- | --- |
| `workout_uuid` | route, effort score | the linked workout, when HealthKit reports the link |
| `ecg` | ECG | `classification`, `symptoms_status`, `lead` (raw enum values), `average_heart_rate` (count/min), `sampling_frequency_hz`, `voltage_count`, `voltage_unit` (`mcV`), `voltages[]`; `offsets_s[]` only if the spacing is not 1/frequency |
| `beats` | heartbeat series | `count`, `offsets_s[]` (time since series start), `preceded_by_gap[]` |
| `route` | route | `count` and parallel arrays: `offsets_s` (since sample start), `latitude`, `longitude`, `altitude_m`, `ellipsoidal_altitude_m`, `horizontal_accuracy_m`, `vertical_accuracy_m`, `speed_mps`, `speed_accuracy_mps`, `course_deg`, `course_accuracy_deg`. These are the CoreLocation values as given (negative = invalid). Floor and source information are not sent |
| `workout.stats` | workout, activity | `{type: {avg, min, max}}` in registry units (e.g. heart rate) |
| `workout.events` | workout, activity | `{type, start, end, metadata}` with the raw `HKWorkoutEventType` |
| `workout.activities` | workout | `{uuid, activity_type, location_type, swimming_location_type, lap_length_m, start, end, duration_s, totals, stats, events, metadata}` |
| `state_of_mind` | State of Mind | `kind`, `valence` (−1 to 1), `valence_classification`, `labels[]`, `associations[]` (raw enum values) |
| `activity_summary` | summary | `date`, `move_mode`, `paused`, `active_energy_kcal`, `move_time_s`, `exercise_time_s`, `stand_hours`, each with its `…_goal` |

Raw enum values are sent as in v1 and mapped to words by the normalizer. **Activity summaries** have no HealthKit identity, so the contract gives them one:
- `uuid` = UUIDv5, namespace `beff15b8-8908-4179-976c-1ebb715deb23`, name `activity_summary:<YYYY-MM-DD>`. It has no device id, so two phones of the same Health account produce the same key.
- `start` and `end` bound the local day.
- `source_revision` is the fixed marker `{"bundle_id": "vitamux.activity-summary", "name": "Activity summary"}`, because HealthKit reports no source.
- `anchor.before_hash` = `after_hash` = SHA-256 of the page's `activity_summary` objects (sorted keys). The idempotency key therefore changes exactly when a day changes: an unchanged re-read is a duplicate, and a changed day is a new raw version.
- `deleted` is always empty.

### Storage: no new tables
| Payload | Rows | Key and notes |
| --- | --- | --- |
| quantities | `measurements` | type + UUID, as in v1 |
| `beats` | `rr_interval` samples (s) at series start + offset: one per beat after the first, skipping beats `preceded_by_gap`. Not resolvable | `<uuid>#<beat index>`; a tombstone of the series UUID withdraws all of them |
| `ecg` | `health_events` `ecg_recording`: level = classification word, value = average HR (bpm), `context` = symptoms status, frequency, count, lead, algorithm version | `file_blob_sha256` → `vitamux.waveform/1` (`start`, `sampling_frequency_hz`, `unit` µV, `lead`, `values`, optional `offsets_s`) |
| `route` | `health_events` `workout_route`, `context` = `workout_uuid`, point count | `file_blob_sha256` → `vitamux.route/1` (the payload `route` object plus `start`). The workout finds it by `workout_uuid` = `workouts.external_id`, else by time inside the workout from the same origin. Arrival order does not matter and the workout row is never rewritten |
| `workout.*` | `workouts` as in v1; heart-rate `stats` fill `avg_hr_bpm` and `max_hr_bpm`. `workout_segments`: lap events → `lap`, segment events → `interval`, activities → `activity` (totals and stats in `data`), pause to resume and motion-pause pairs → `pause`, markers and pause or resume requests → `marker` | segments are replaced with their workout, as in v1 |
| effort scores | `measurements` interval, provider-scoped `latest` | `context.workout_uuid` |
| `activity_summary` | `measurements` daily values, `local_date` = `date`, `context` = goal, move mode, paused | `(uuid, code)`; a changed day supersedes. Origin is the marker, native Apple |
| `state_of_mind` | `health_events` `state_of_mind`: value = valence, level = kind word, `context` = classification, label and association words | type + UUID |
| categories | `health_events` with code-owned level words; HealthKit metadata in `context` | type + UUID |

J22.17 migrates `health_events.file_blob_sha256` (references `blobs`, counted like `workouts.file_blob_sha256`) and widens the `workout_segments.kind` check to add `activity`, `pause` and `marker`. `move_time` gains the daily kind. The normalizer writes blobs from raw. They are content-addressed, so a reprocess with the same content rewrites nothing. The canonical row holds the reference, so a blob outlives raw retention, and it is released when the row is purged.

### Read endpoints
- `GET /api/v1/events/{id}/waveform` and `GET /api/v1/workouts/{id}/route` return the blob document as `application/json`. The `ETag` is the blob's SHA-256. They answer 404 when there is no waveform or route. Both need `read:health` and are listed in `api/openapi.yaml` and `api/authz.yaml`.
- Exports include both documents. Deletion and `purge-user` remove them with their row.

### Privacy opt-in
1. ECG, beats, routes, cycle, symptoms and mind are separate groups, off on install and after the upgrade from Bridge. Nothing in them is requested or read before the owner turns that group on. One group never turns on another; routes need workouts to be on, and the toggle says so.
2. Turning a group on first shows a short plain sheet: what is read, and that it is stored on the owner's own server without app-level encryption, relying on disk encryption ([security](../architecture/security.md#threat-model)). For routes, the sheet adds that locations show where each workout went and that the app's map loads Apple map tiles for that area. Then the HealthKit sheet asks for that group's types only.
3. Turning a group off stops its queries and observers. Server data stays until the owner deletes it.
4. Widgets, notifications and logs never contain values, labels, coordinates or voltages from these groups. The redaction tests gain route and ECG sentinels (J22.17). The server never sends routes or waveforms to a third party, and the panel draws routes without map tiles.
5. Values are shown as recorded, never interpreted: the ECG classification is Apple's label, and cycle and rhythm alerts are named as alerts. Fixtures stay synthetic, and fixture routes sit at sea near 0°, 0°.

### What stays out
- **Clinical records, verifiable records and CDA documents**: these need the Health Records capability (`com.apple.developer.healthkit.access`), with its own review, and are not Watch data.
- **Medications and dose events** (iOS 26): authorized per medication through `HKUserAnnotatedMedicationType`, not per type. **Vision prescriptions**: per-object authorization. Each needs its own design.
- **Audiograms, GAD-7, PHQ-9 and toothbrushing**: written by the iPhone, AirPods or accessories, not the Watch. They stay `later` in the catalogue.
- **Characteristics** (birth date, sex, blood type): profile facts, not samples.
- **Live workout data** (`HKLiveWorkoutBuilder`, zone updates): only available to an app running the workout session on the Watch.
- **Ovulation estimates**: no HealthKit type exists.

## Alternatives considered
- **A new stream `healthkit.samples.v2`**: every change is additive and optional, so the v1 contract holds and old apps keep working.
- **Routes in `workouts.file_blob_sha256`** (the earlier draft): a route can arrive before its workout, and a route deletion would rewrite the workout row. That column stays for original activity files (FIT, GPX).
- **ECG voltages as `measurements` rows**: about 15,000 rows per recording that look resolvable. A waveform is one document.
- **Beats as a blob on an event**: RR values are a time series drawn like other metrics, at a few hundred rows a day.
- **Regrouping v1 types into Fitness, Mobility and Hearing groups** (the earlier draft): this would change the prompts and defaults of types that are already authorized, for no gain in privacy.
- **Route points as objects**: about three times larger than parallel arrays.
- **A per-device activity-summary key**: two phones of one Health account would store every day twice.

## Consequences
- J22.16 builds `Registry.v2`, the readers and the summary re-read. J22.17 builds the catalogue codes, the migration, the normalizer, the blobs and the endpoints. J22.18 draws the views. The v1 kit keeps working against the extended schema.
- `TestHealthKitSamplesSchema` (`internal/ingest`) keeps the v1 fixtures and the Watch examples valid.
- Owner choices to confirm: routes as events (not on the workout row); Apple's RMSSD sharing `hrv_rmssd`; the summary marker origin; no per-group server deletion yet.
