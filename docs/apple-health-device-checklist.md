# Apple Health device checklist

Run before a v0.2.0 release ([J15.7](plan/E15-apple-health/J15.7-device-campaign.md)) on at least one **physical iPhone**: background delivery does not run in the Simulator, and several facts below are undocumented by Apple ([apple-health.md](architecture/apple-health.md#sync-algorithm)). Use a test Health store or an owner who accepts the access. Do not run on someone else's data.

**Recording rule:** write down counts, durations, timestamps, type identifiers, bundle ids, HTTP statuses and pass/fail. Never record a health value, a sample UUID, a token or a pairing code, here or in issues.

## Setup

- Note the iPhone model, iOS version, build id, account type (paid or free), and whether Low Power Mode and Background App Refresh were on.
- Use a throwaway Vitamux instance with a fresh pairing code per run. Enable the app's delivery log: one line per observer callback with type, `observer_fired_at`, newest sample `end`, `upload_acked_at` and status. No values.

## Checks

| ID | Check | Steps | Expected | Record |
| --- | --- | --- | --- | --- |
| D1 | First authorization | Pair, enable one group, grant some types and deny others | Sheet lists only the enabled group; denied types look empty; app shows "requested", not "granted"; denied types are flagged "possibly denied" after 7 silent days | Which types were denied, what the app showed, time to first upload |
| D2 | Large backfill | Enable a group with at least 6 months of data, including heart rate | Pages of at most 2,000 samples or 1 MiB gzip; the anchor advances only after `202`; the app survives suspension mid-backfill and resumes | Total sample count, batch count, wall time, peak memory if measured, any 413/429/5xx |
| D3 | Background delivery while locked | Enable hourly delivery for several types, leave the app suspended (not force-quit), lock the phone, and generate new samples (walk, wear the watch) | Observer fires, app uploads, completion handler is called; note when the first callback after new data arrives and whether data is readable while locked | Delivery log per type: lag from sample `end` to `observer_fired_at` and to `upload_acked_at`; first-unlock effect; Low Power Mode effect |
| D4 | Delivery after force-quit and reboot | Force-quit the app, then reboot and unlock | Record whether delivery resumes without opening the app | Resumed (yes/no), after how long |
| D5 | Revoke and re-grant | Revoke one type in Settings > Health > Data Access, wait, re-grant. On iOS 27 also grant a limited window | Revoked type goes silent without an error; re-grant resumes with no loss; a limited window is reported per type | What the app and server showed; whether a re-pull happened; duplicates (expect none) |
| D6 | Deletion in Health | Delete one sample of a quantity type, one blood pressure reading, one sleep entry in the Health app | Tombstones reach the server; the BP group and its members are deleted together; the observer fires on deletion | Which deletions arrived, how long they took, and whether the BP correlation or only its members were reported |
| D7 | Airplane-mode retry | Turn airplane mode on during an upload, then off | Anchor unchanged; the same batch is re-sent with the same idempotency key; the server answers `202` or `409`; no duplicates | Statuses seen, retry count, backoff intervals |
| D8 | Token revoke | Revoke the device in the web UI during a sync | Next upload gets `401`; the app shows a re-pair state and stops retrying; no data lost; the anchor is kept | Time to detect, app message |
| D9 | Reinstall | Delete and reinstall the app | Record whether the Keychain token survives; anchors are lost, so a full pull follows and is deduplicated by UUID | Token survived (yes/no), duplicate rows after the re-pull (expect 0) |
| D10 | Source bundle ids | Connect the brand apps the tester owns and read the origins list | Each brand appears as an origin with its bundle id | Observed bundle id and device name per brand, compared with the [brand table](architecture/apple-health.md#brand-origin-sets); which types it wrote |
| D11 | Free-account build | Build with a free Personal Team, if available | Record whether the Background Delivery capability and entitlement are accepted, and when the profile expires | Accepted (yes/no), error text, days until expiry; closes the open point in [Distribution](architecture/apple-health.md#distribution) |
| D12 | Sleeping breathing disturbances unit | On a watch with sleep apnea notifications, read `AppleSleepingBreathingDisturbances` for a few nights | The bridge reads it as `count` and the catalogue stores it as `events/h` ([`breathing_disturbances`](metrics.md)); confirm the value is Apple's per-hour index and not a night total | Unit string the sample carries, whether values look like a rate or a count (no values), the source of truth used |
| D13 | ECG ([ADR-0024](adr/0024-watch-data.md)) | Turn on ECG, record one ECG on the Watch, then delete an older one in Health | One page per up to 5 recordings; the deletion tombstones the event | Identifier HealthKit reports for the ECG type, voltage count, sampling rate, whether the spacing was even (no `offsets_s`), the algorithm-version metadata key as sent (the SDK constant's value is `HKMetadataKeyAppleECGAlgorithmVersion`), whether any measurement lacked lead I (the kit then fails the page), page gzip size |
| D14 | Beats and RMSSD | Turn on beats, wear the Watch overnight | One heartbeat series per HRV reading | Series count, beats and gaps per series, whether each series shares start and end with an HRV SDNN sample; on iOS 27, whether the Watch writes `HeartRateVariabilityRMSSD` and over what span |
| D15 | Route | Turn on routes, record an outdoor workout, later delete one workout in Health | Route page carries `workout_uuid`; the panel finds it from the workout | Whether the route arrived before or after its workout, points per minute, page gzip size, whether deleting the workout also deleted its route, whether `workout_uuid` was found (the kit looks at workouts overlapping the route) |
| D16 | Activity summary | Sync several times in one day, pause rings for a day, travel across a time zone if possible | Today's day is re-sent only when it changes; earlier days stay duplicates | Identifier HealthKit reports for the summary type, days returned, move mode, `paused`, whether the day after a zone change carries the phone's calendar date, how many pages the first backfill took |
| D17 | Workout detail and effort | Record a multisport or interval workout with laps and a pause, rate its effort afterwards | Events and activities arrive with the workout; the effort sample carries `workout_uuid` | Event and activity counts (including whether a single-activity workout reports one activity, as the synthetic tests see), whether the effort sample spans the workout, whether a later rating adds a sample or deletes the estimate |
| D18 | Background delivery of new types | Register observers for ECG, heartbeat series, routes and State of Mind; log a State of Mind on the Watch | `enableBackgroundDelivery` succeeds or fails without breaking the other types | Per type: accepted (yes/no), error text, callback lag; State of Mind type identifier (`HKDataTypeStateOfMind` expected) and source bundle id |
| D19 | Notification refresh ([J22.21](plan/E22-ios-app/J22.21-notifications.md)) | Allow notifications in Settings › This app, leave the app in the background for a day with Background App Refresh on, then break a condition (pause a backup or revoke a test connection) | iOS runs the refresh now and then; a changed condition notifies once, and a tap opens its screen | Background checks count and last time from Settings › This app, time from the change to the notification, whether a tap from the lock screen opened the right screen |

## Results

Copy one block per run. Leave health values out.

| Date | iPhone / iOS | Build | Account | Check | Result (pass, fail, partial) | Observation | Follow-up |
| --- | --- | --- | --- | --- | --- | --- | --- |
| | | | | D1 | | | |
| | | | | D2 | | | |
| | | | | D3 | | | |
| | | | | D4 | | | |
| | | | | D5 | | | |
| | | | | D6 | | | |
| | | | | D7 | | | |
| | | | | D8 | | | |
| | | | | D9 | | | |
| | | | | D10 | | | |
| | | | | D11 | | | |
| | | | | D12 | | | |
| | | | | D13 | | | |
| | | | | D14 | | | |
| | | | | D15 | | | |
| | | | | D16 | | | |
| | | | | D17 | | | |
| | | | | D18 | | | |
| | | | | D19 | | | |

Observed behaviour that differs from [apple-health.md](architecture/apple-health.md) goes into the known-limitations doc (J15.7, T15.7.4), and the architecture doc is corrected in place.
