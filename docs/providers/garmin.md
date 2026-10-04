# Garmin Connect

Facts about the unofficial Garmin Connect web services as reached through [`garminconnect`](https://github.com/cyberjunky/python-garminconnect) 0.3.17, and how Vitamux uses them. Read from the library source on 2026-10-04 (J18.1); what no one has run against a real account yet is marked **unverified** and is covered by the [owner checklist](#manual-real-account-checklist). Decision record: [ADR-0018](../adr/0018-garmin-upstream.md). Code: [`sidecars/garmin`](../../sidecars/garmin), protocol `vitamux-connector/1`: [connectors#remote-sidecar-mode](../architecture/connectors.md#remote-sidecar-mode). Unofficial, so `Official=false` and Garmin can change anything without notice.

## Auth

| Step | What happens | Notes |
| --- | --- | --- |
| Sign in | `Garmin(email, password, return_on_mfa=True).login()` tries up to five strategies in order (mobile SSO with and without `curl_cffi` TLS impersonation, SSO widget, web portal) and accepts the first whose token the API tier accepts | A wrong password stops the chain with `GarminConnectAuthenticationError`; 429s and transport errors fall through to the next strategy |
| MFA | Garmin sends a code (email or app, per account). `login()` returns `("needs_mfa", None)`; `resume_login(None, code)` completes it | The pending state (HTTP session, cookies, CSRF) lives on the client object and **cannot be serialized**. The sidecar keeps it in memory for 10 minutes (at most 16 pending) and returns only a random key as the opaque `session`; a restart in between means signing in again. `auth/begin` asks for email and password without calling Garmin; the first `auth/continue` signs in and then asks for the code (a `code` prompt field) A wrong code keeps the pending session, so a retry works (upstream behaviour since 2026-08-21) |
| Session | DI OAuth2 bearer tokens from `diauth.garmin.com`: `di_token`, `di_refresh_token`, `di_client_id` | `dumps()` returns exactly these three and `loads()` restores them, so the sidecar is stateless. Credentials on the wire: `access_token` is `di_token`, `refresh_token` is `di_refresh_token`, and `extra` holds `di_client_id` plus `display_name` and `profile_id`, so a fetch needs no profile calls. A session that only yielded the JWT_WEB cookie fallback cannot be persisted: `permanent` |
| Refresh | The library refreshes the access token before a request when its JWT `exp` is under 15 minutes away, and once after a 401. `/v1/auth/refresh` forces it through `Client._refresh_di_token()` (upstream-private) | The response may carry a new refresh token; a fetch returns rotated credentials in its `result` line. The protocol has no place for them on an error, so a fetch that rotates and then fails loses them and the next call refreshes again |
| Account id | `GET /userprofile-service/socialProfile` gives `profileId` (integer) and `displayName` (a UUID) | The core stores only a hash of `account_id` |

Unverified: the access-token lifetime (readable from the JWT `exp`), the refresh-token lifetime (upstream: "indefinitely as long as the refresh token remains valid"), whether refreshing rotates the refresh token, and whether a Garmin password change revokes it. Treat any refusal as `reauth_required`.

## Raw access

`Garmin.connectapi(path, params=)` returns the endpoint's parsed JSON unchanged; HTTP 204 comes back as `{}`. `Garmin.download(path)` returns the bytes. The library's helpers (`get_heart_rates`, `get_spo2_data`, ...) reshape or raise on empty days (for example SpO2 `lastSevenDaysAvgSpO2` string to float), so the sidecar calls the endpoint paths itself, with the path constants the library defines. Python re-serializes the JSON, so number formatting such as `1.0` survives but key order does not: raw bodies are emitted with sorted keys.

## Endpoints per stream

`{dn}` is the profile's `displayName`, `{d}` a local calendar date. All are `GET` under `https://connectapi.garmin.com`.

| Stream | Endpoint and parameters | Unit | Top level | Granularity |
| --- | --- | --- | --- | --- |
| `garmin.daily_summary` | `/usersummary-service/usersummary/daily/{dn}` `calendarDate={d}` | day | object | Day totals; also resting HR, stress and Body Battery summary fields |
| `garmin.heart_rate` | `/wellness-service/wellness/dailyHeartRate/{dn}` `date={d}` | day | object | Intraday HR samples (`heartRateValues`) |
| `garmin.steps` | `/wellness-service/wellness/dailySummaryChart/{dn}` `date={d}` | day | array | Intraday step buckets |
| `garmin.stress_body_battery` | `/wellness-service/wellness/dailyStress/{d}` | day | object | Stress samples (`stressValuesArray`) and Body Battery samples (`bodyBatteryValuesArray`) |
| `garmin.sleep` | `/wellness-service/wellness/dailySleepData/{dn}` `date={d}`, `nonSleepBufferMinutes=60` | day | object | One sleep with stages, SpO2, respiration and HRV during sleep |
| `garmin.hrv` | `/hrv-service/hrv/{d}` | day | object | Nightly summary and readings |
| `garmin.respiration` | `/wellness-service/wellness/daily/respiration/{d}` | day | object | Intraday respiration |
| `garmin.spo2` | `/wellness-service/wellness/daily/spo2/{d}` | day | object | Intraday SpO2 |
| `garmin.training` | `/metrics-service/metrics/maxmet/daily/{d}/{d}` (key `:vo2max`), `/metrics-service/metrics/trainingreadiness/{d}` (key `:readiness`) and `/metrics-service/metrics/trainingstatus/aggregated/{d}` (key `:status`) | day | object or array; array; object | VO2max, training readiness, and the training status with acute and chronic load per device |
| `garmin.floors` | `/wellness-service/wellness/floorsChartData/daily/{d}` | day | object | 15-minute rows `[start GMT, end GMT, ascended, descended]` |
| `garmin.hydration` | `/usersummary-service/usersummary/hydration/daily/{d}` | day | object | The day's logged intake (`valueInML`) and estimated sweat loss |
| `garmin.fitness_age` | `/fitnessage-service/fitnessage/{d}` | day | object | Fitness age and its achievable target |
| `garmin.intraday_reload` | `POST /wellness-service/wellness/epoch/request/{d}`, then the intraday streams of that day | day | none | On demand only, see [cold storage](#cold-storage-and-reload) |
| `garmin.body_composition` | `/weight-service/weight/dateRange` `startDate`, `endDate` | 30-day block | object | One entry per weigh-in |
| `garmin.blood_pressure` | `/bloodpressure-service/bloodpressure/range/{start}/{end}` `includeAll=True` | 30-day block | object | One entry per measurement |
| `garmin.activities` | `/activitylist-service/activities/search/activities` `startDate`, `endDate`, `start`, `limit=20`, `sortOrder=asc`; FIT: `/download-service/files/activity/{id}` | activity | array; zip | Summary per activity; the original FIT in a zip |

Sample intervals (verified 2026-10-04): heart rate 2 minutes, stress and Body Battery 3, respiration 2, steps and floors 15, HRV 5, sleep SpO2 1 minute (`wellnessEpochSPO2DataDTOList` in the sleep payload, with a reading confidence; `daily/spo2` carries summaries, hour-aligned averages and mostly null spot lists). Sleep also carries movement, heart-rate, stress, Body Battery, HRV and respiration arrays; the last five duplicate the all-day streams and stay raw.

Unverified: that `maxmet/daily` returns an array (the library types it as an object, so both are accepted), that a day without data is 204, `{}` or `null` for every endpoint (a 404 would be `permanent`), and that `sortOrder=asc` is honoured by the activity search (the library passes it through).

## Time

- Calendar dates (`calendarDate` and the `{d}` parameters) are the user's local dates, as Garmin Connect shows them. The sidecar derives the days of a window in the connection's `config.timezone` (an IANA name, default UTC).
- Sleep: `sleepStartTimestampGMT` and `sleepEndTimestampGMT` are epoch milliseconds in GMT; the `...Local` fields are the same instants shifted by the local offset. Upstream warns that on some accounts (China, UTC+8) the local fields are offset twice, so normalizers should prefer GMT.
- HR, stress and Body Battery carry `startTimestampGMT`, `startTimestampLocal` and the matching end fields; activities carry `startTimeGMT` and `startTimeLocal`; training readiness carries `timestampLocal`.
- The timezone itself is not in these responses. **Unverified**: whether `/userprofile-service/userprofile/user-settings` reports it. Vitamux uses the owner's configured timezone periods, and the local minus GMT difference where a payload has both.

## Mapping

Every value Garmin sends reaches a catalogue code unless it is an identifier, a flag, UI text or exactly derivable from stored rows (min, max and average of a stored series, a window's weekly mean); each stream's `testdata/<stream>/fields.json` records the reason per field (J25.5). Garmin-only values have provider-scoped `garmin_*` codes ([metrics](../metrics.md)): heart-rate zone times, training effects and load per activity, HRV baseline, training-load range, recovery time, cycling VO2max, metabolic age, physique rating, sweat loss, sleep movement, achievable fitness age. Per-minute sleep SpO2 are `spo2` samples; the night's average is `spo2_nightly`, taken from `daily/spo2` only (`avgSleepSpO2`), never also from the sleep summary. Training status labels (status phrase, readiness `inputContext`) are text and stay raw. Activity summaries map their averages (speed, grade-adjusted speed, power, cadence, stride and stroke length, vertical oscillation, ground contact, respiration, swolf, swim cadence, stress), extremes (max speed, power, cadence and stress, temperatures, elevations, respiration), power-zone times, fastest splits, intensity minutes, estimated sweat loss, BMR calories, strokes, sets and reps, stress at start and end, body-battery change and VO2max as measurements over the activity (training effects and VO2max at its end). Speed and power go to the shared sport codes (`speed_running`, `speed_cycling`, `speed_walking`, `speed_rowing`, `power_running`, `power_cycling`) and otherwise to `garmin_activity_avg_speed` and `garmin_activity_avg_power`; cadence, stride and the other shared fields use their shared codes, the rest `garmin_activity_*`. The activity search library keeps no payload samples, so the key names were checked against public recorded `activitylist-service` responses (`garth` test cassettes `test_activity_list*`, `test_activity_update*`: a trail run, a pool swim and a yoga session, 2026-10-05); an absent field is skipped, a retyped one is `schema_drift`. Verified there: every mapped key except the four below. Not seen in any public payload, kept from the library's activity model or memory (**unverified**): `averageBikingCadenceInRevPerMinute`, `maxBikingCadenceInRevPerMinute`, `fastestSplit_5000` and `fastestSplit_10000`; `totalSets` and `totalReps` come from the `Activity` model of `python-garminconnect` 0.3.17 (`typed.py`), not from a payload. `differenceStress` is derivable and `maxDoubleCadence` repeats the running cadence maximum, so neither is stored. Fields no recorded payload has populated (`continuousReadingDTOList`, the load balance) stay raw until a real shape is seen.

## Cold storage and reload

Garmin keeps intraday detail online for a few months; older days answer with empty series while daily summaries stay. `POST /wellness-service/wellness/epoch/request/{date}` asks Garmin to restore one day for about a week and answers `SUBMITTED`, or `DENIED` after roughly 30 requests a day (community findings, not documented by Garmin).

Vitamux offers this as an opt-in slow backfill: pick the stream `garmin.intraday_reload` in the Backfills tab of the Garmin connection. It has no schedule and runs only as a backfill, one day per unit, paced to 20 units a UTC day by the core (a paced backfill, [connectors](../architecture/connectors.md#runtime-responsibilities)). A unit requests the reload, polls the heart-rate endpoint (every 20 s, up to six times; `VITAMUX_GARMIN_RELOAD_WAIT_S`) until the day is back, then fetches the day's intraday streams as ordinary raw lines of those streams, which normalize as usual. `DENIED` ends the call as `rate_limited` until the next UTC midnight: the unit's job is rescheduled and nothing retries sooner. The pause is scoped to this stream: the core only blocks the whole provider for a rate limit on a scheduled stream, so the other Garmin streams keep syncing. Unverified: how quickly a reloaded day is readable, and the daily limit itself.

## Recalculation and lookbacks

Unverified, so deliberately generous. A watch can sync hours or days late and Garmin then fills in or recalculates that day (sleep scores, HRV status, Body Battery, daily totals, training readiness). Lookbacks start at 72 hours for daily streams and activities and 168 hours for training and the range streams; the owner checklist includes a late-sync test, and the numbers move to measured ones in J18.5. Activities can be renamed or retyped at any time; a changed summary is a new raw version.

## Limits

- No published limits. The library notes that Garmin (behind Cloudflare) may block a client or network instead of answering 429; login is the most exposed call. The library drops `Retry-After`, so the sidecar reports 300 seconds unless a response carries one.
- Sidecar pacing: 1 second after every Garmin call (`VITAMUX_GARMIN_CALL_DELAY_S`), at most 31 units per fetch call, and a core bucket of 2 requests per 4 seconds (`describe` `rate_limits`). About 9 calls per day of history, so a year takes roughly 1.5 hours and five years about 8 hours.
- Account lock after repeated failed sign-ins: **unverified**. The sidecar never retries a login itself; the core stops a connection at `needs_reauth`.

## How Vitamux syncs

- Protocol `vitamux-connector/1` ([ADR-0017](../adr/0017-sidecar-protocol.md)). Correction and backfill send a window (`from`, `to`) and the sidecar fetches its local days; an incremental or manual run sends only the stream cursor `{"since": <day>}` and fetches from that day (default: two days back) through today in `config.timezone`, so the last day is re-fetched each run. A finished incremental run stores `{"since": <today>}`; a finished window run leaves the cursor alone. The core re-fetches each stream's lookback as correction windows. Pages inside a call add `{"day"}` (next unit) or `{"offset"}` (activities) to the cursor. Streams are the `garmin.*` names above, scheduled hourly (`garmin.activities` every 2 hours; training and the range streams every 6 hours).
- Raw: one line per day, or per aligned 30-day block for body composition and blood pressure (so a re-fetch has the same key). Body `{"unit": {...}, "response": <endpoint JSON verbatim>}`, with `unit` being `{"date"}`, `{"start","end"}` (both inclusive) or `{"activity_id"}`, and `request` carrying the endpoint and parameters. External key `<stream>:<date | start_end | activity_id>`, plus `:vo2max`, `:readiness` or `:fit`. An empty day is stored as it came (`{}` or `null`). Activities page by offset in `next_cursor` over the window's days; the FIT zip is a binary line (`application/zip`), absent for manual activities that have no file.
- Errors: a refused or expired session is `reauth_required`; HTTP 429 is `rate_limited`; 5xx and network errors are `transient`; a body that is not JSON, or a top-level type other than the stream's, is `schema_drift` (with the `endpoint` and a `fingerprint`, a hash of the response's keys and kinds, never values); anything else (403, 404, bad request) is `permanent`. If a fetch fails after some units succeeded, it returns them with a cursor to resume instead of an error. Passwords, codes, tokens and bodies are never logged or echoed.
- Normalizers are Go ([J18.4](../plan/E18-garmin/J18.4-normalizers.md)); Apple Health relays of Garmin data stay distinguishable by origin.

## Manual real-account checklist

The owner runs this once with their own Garmin account (E18 acceptance). Never paste passwords, codes, tokens, ids or values into issues, logs or docs; record only pass or fail, field names and shape fingerprints.

1. Start the sidecar with a secret file and connect Garmin in the panel: email, password, MFA code. Expect an active connection. Try a wrong code first; expect to be able to retry.
2. Begin a sign-in, wait 11 minutes, send the code. Expect a clear "sign in again". Restart the sidecar between begin and continue; same result.
3. Let the first backfill run. Expect raw for every stream, day by day, without 429s. For each stream record the top-level type and the key list of one day (names only), and whether empty days come back as 204, `null`, `{}` or 404.
4. Confirm `garmin.training` returns the VO2max body as an array or an object, and that the activity search lists oldest first.
5. Finish an activity and sync the watch; expect the activity and its `:fit` raw in the next sync. Rename it in Garmin Connect; expect a new raw version.
6. Leave the watch unsynced for a day, sync it late, and note which earlier days changed and how many days back (this sets the lookbacks).
7. Leave Vitamux running for more than a day; expect syncs to continue without a new sign-in (token refresh). Note the access-token `exp` window from the JWT, never the token.
8. Change the Garmin password; expect the next sync to show *needs reauthorization*, then sign in again and expect no gaps.
9. Check the logs of both containers: no password, code, token, id or body.
