# WHOOP

What the unofficial `@dofek/whoop` client and WHOOP's private app API do, and how Vitamux uses them (J19.1, source review 2026-10-04). The sidecar is in [`sidecars/whoop`](../../sidecars/whoop); the decision is [ADR-0019](../adr/0019-whoop-upstream.md); the protocol (`vitamux-connector/1`) is in [connectors.md](../architecture/connectors.md#remote-sidecar-mode).

**How this was checked.** Read the published package (`@dofek/whoop` 0.1.65: `client.js`, `types.d.ts`, README), the upstream repo's provider code that calls it, and its own OpenAPI notes (`docs/whoop-api.openapi.yaml`, "last updated March 2026"). On 2026-10-04 the owner's live instance confirmed the key paths and value kinds (never values) of cycles, sleep events, heart rate, workouts and the strain deep dive; [Content of records](#content-of-records) is that shape. Other **unverified** items are marked, and the [owner checklist](#manual-real-account-checklist) closes them. WHOOP can change any private endpoint without notice.

Sources: [package source](https://github.com/Asherlc/dofek/tree/main/packages/whoop-whoop), [provider code](https://github.com/Asherlc/dofek/tree/main/src/providers/whoop), [private API notes](https://github.com/Asherlc/dofek/blob/main/docs/whoop-api.openapi.yaml), and for contrast WHOOP's [official OAuth docs](https://developer.whoop.com/docs/developing/oauth).

## Assumptions checked

| Assumption | Result |
| --- | --- |
| Cognito sign-in with app, SMS or email codes | **Confirmed in code**: `InitiateAuth` `USER_PASSWORD_AUTH` through WHOOP's proxy, answered with `RespondToAuthChallenge`. **Confirmed by the owner (2026-10-04)**: WHOOP also emails codes (Cognito `EMAIL_OTP`), which `@dofek/whoop` 0.1.65 answers as `SMS_MFA` and Cognito refuses. The sidecar therefore records the challenge Cognito names and answers `<CHALLENGE>` with `<CHALLENGE>_CODE`. |
| Rotating refresh tokens | **Corrected**: `REFRESH_TOKEN_AUTH` is observed *not* to return a new refresh token; the client reuses the old one. The sidecar still stores whatever comes back, so rotation would need no change. |
| Token lifetimes | **Unverified**: the access token's life is Cognito's `ExpiresIn` (the client stores it, the value is not documented; typically one hour). The refresh token's life is unknown. The sidecar refreshes 60 s before expiry, and once after a 401. |
| Response JSON can be kept verbatim | **Corrected for the client's return values, solved in the sidecar**: `getCycles` unwraps or normalises the body, `getMetricValues` returns only `values`, `listDeveloperWorkouts` re-builds `{records, next_token}` after a zod parse (`passthrough`, so extra fields survive, but `next_token` is defaulted). The other methods return `response.json()` as is. The sidecar therefore wraps the injected `fetch` and keeps the **response text** of each request, which is embedded unparsed in the raw: byte-exact, large integers and key order included. |
| Zod hides drift | **Confirmed, then corrected (2026-10-04)**: only the developer workout page is zod-validated. Its schema refuses `null` in optional fields, and older workouts carry `sport_id: null`, so a whole page failed. On a `ZodError` the sidecar now reads the page from the captured body with its own checks (`records` array, record `id`, `start`). For the other methods the sidecar checks the minimum shape itself (see [errors](#errors-and-limits)). |
| 6 s heart rate is available | **Confirmed in code** (`step` defaults to 6 for `getHeartRate`); how far back and how much per request is **unverified**. |
| `WHOOP_API_THROTTLE_MS` | **Confirmed**: 1000 ms, advisory only. The client neither delays nor retries 429. |

## Auth

- Base: `https://api.prod.whoop.com`, auth proxy `/auth-service/v3/whoop/` with header `X-Amz-Target: AWSCognitoIdentityProviderService.<action>` and a public app client id baked into the client. Data requests carry `Authorization: Bearer <access token>`, `User-Agent: WHOOP/4.0` and the query `apiVersion=7`.
- Sign-in with email and password returns tokens (no MFA) or `ChallengeName` plus `Session`. The code is submitted with the session, the email and the challenge Cognito named. A wrong code gives Cognito `CodeMismatchException`; an expired session `NotAuthorizedException`.
- The numeric WHOOP user id comes from `GET /users-service/v2/bootstrap/?accountType=users&apiVersion=7&include=profile` (`user.id`). It is the account id (`account_id`) and is part of the metrics and cycles requests. After a refresh it is fetched best-effort; the stored one is kept if that fails.
- `auth/begin` asks for email and password and does not call WHOOP. The first `auth/continue` signs in; with MFA it returns a `code` prompt, and the MFA session (Cognito session, email, method, challenge) is sealed into the opaque `session` (base64) the core holds until the second `auth/continue`.
- Credentials on the wire: `access_token`, `refresh_token`, `expires_at` (RFC 3339) and `extra: {user_id}`.

## Data: methods and endpoints

All GET unless noted. `start`/`end` are RFC 3339 instants. Pagination exists only on the developer workout list.

| Stream | Client method | Endpoint | Parameters | Notes |
| --- | --- | --- | --- | --- |
| `whoop.heart_rate` | `getHeartRate(start, end, step=6)` | `/metrics-service/v1/metrics/user/{userId}` | `name=heart_rate`, `start`, `end`, `step` | Response `{values: [{time (unix ms), data (bpm)}]}`. 6 s step. A 400 or 404 means the metric was rejected (`WhoopMetricUnavailableError`). |
| `whoop.cycles` | `getCycles(start, end, limit=200)` | `/core-details-bff/v0/cycles/details` | `id`=user id, `startTime`, `endTime`, `limit` | `{records: [...]}` (confirmed). Window at most 200 days (private notes, unverified). No page token: windows are kept small. |
| `whoop.sleep` | `getSleep(id)` | `/sleep-service/v1/sleep-events` | `activityId` | One sleep per call: an array of stage events (confirmed); ids are the window's `sleeps[].activity_id`. |
| `whoop.workouts` | `listDeveloperWorkouts({limit, nextToken})` | `/developer/v2/activity/workout` | `limit`, `nextToken` | `{records, next_token}`; no time filter, newest first (as the upstream provider assumes). Records have a UUID `id`, `start`, `end`, `timezone_offset`, `sport_name`, `sport_id`, `score_state`, `score`. Omits workouts deleted in WHOOP. |
| `whoop.workouts` detail | `getWeightliftingWorkout(id)` | `/weightlifting-service/v2/weightlifting-workout/{id}` | none | 404 means no strength data (the client returns `null`). Holds `workout_groups[].workout_exercises[]` with `sets` (weight, reps, volume, time, `during`, `complete`) and `exercise_details`, plus MSK and cardio strain scores and `zone_durations`. |
| `whoop.strain_deep_dive` | `getStrainDeepDive(date)` | `/home-service/v1/deep-dive/strain` | `date=YYYY-MM-DD` | An app screen: `header`, `metadata`, `sections[].items[]` of display tiles (titles, gauges, formatted text). Only the day's step total is read (below); the rest stays raw. |
| `whoop.journal` | `getJournal(start, end)` | `/behavior-impact-service/v1/impact` | `startTime`, `endTime` | Untyped, shape varies (array or wrapped `impacts[]` with `answers[]`). **Not synced yet** (see below). |

History depth and the largest window the metrics service accepts are **unverified**: the upstream provider fetches HR in 7-day windows, Vitamux uses 1 day.

**No steps stream.** The metrics service answers `name=steps` with 400 "query param name must be one of [heart_rate]" for every step size (owner's instance, 2026-10-04), so `getSteps` cannot work and `whoop.steps` was removed from the sidecar and the core. Steps from other sources are unaffected. WHOOP's step count still arrives as a **daily total**: the strain deep dive lists it as the metric `CONTRIBUTORS_TILE_STEPS`, whose `status` is display text such as "7,421" (the same tile dofek reads). Normalizer `whoop.strain_deep_dive` v2 writes it as `steps`, kind `daily_value`, for the local day of the raw's unit; an unreadable status gives the warning `steps_unreadable` and no row. There are no intraday WHOOP steps.

## Content of records

- **Cycle record** (`records[]`, confirmed): `cycle` (`id` number, `days` and `during` range strings, `timezone_offset`, `day_strain`, `day_kilojoules`, day heart rates, `data_state`), `recovery` or null (`activity_id`, `state`, `calibrating`, `recovery_score`, `resting_heart_rate`, `hrv_rmssd` **in seconds**, `spo2`, `skin_temp_celsius`, components and baselines), `sleeps[]` (every sleep of the cycle, naps included: `activity_id` string, `during`, `timezone_offset`, `is_nap`, `significant`, `score` (sleep performance), `respiratory_rate`, stage and wake durations in ms, sleep need and debt), `workouts[]` (`activity_id`, `during`, heart rates, kilojoules, intensity), `v2_activities[]` (`id`, `type`, `score_type`, `during`, `timezone`). There is no `cycle.sleep` object and no main sleep id.
- **Sleep** (`sleep-events`, confirmed): an array of stage events `[{during, type}]`, nothing else. Types are mapped as `@dofek/whoop` reads them, in any case: `awake`, `light`, `deep` and `slow_wave` (deep), `rem`; `no_data` is a gap. Any other type is a warning and a gap, not drift.
- **Heart rate**: `{name, start, values: [{time (unix ms), data}]}`.

## Time

- Ranges (`during`) are Postgres range strings: `['2026-03-12T21:37:00.000Z','2026-03-12T21:56:00.000Z')`. `days` is a date range string. Cycles, sleeps and workouts carry `timezone_offset` (`+03:00`); HR carries only UTC instants.
- A WHOOP **cycle** runs from one sleep to the next, so it does not match a calendar day. Vitamux does not use `days` for local dates: it takes instants plus WHOOP's offset, else the owner's timezone periods ([data-model](../architecture/data-model.md)). How cycle boundaries move on travel days is **unverified**.
- `getStrainDeepDive` takes a date; which day boundary WHOOP uses for it is **unverified**. The sidecar passes the owner's local date (from the connection's `config.timezone`, default UTC); the upstream provider passes the UTC date.

## Errors and limits

| Situation | Sidecar class |
| --- | --- |
| Cognito `NotAuthorized`, `CodeMismatch`, `ExpiredCode`, `UserNotFound`; data 401 or 403 after one refresh | `reauth_required` |
| HTTP 429 (`WhoopRateLimitError`, `retryAfterSeconds` from `Retry-After`), Cognito throttling | `rate_limited` (`retry_after_s` when known) |
| Network errors, timeouts, 5xx, 502 to 504 | `transient` |
| `ZodError`, non-JSON body, missing `values`, changed sample shape, endpoint 404, "no tokens in response", repeated workout page token, a cycles window that reaches its `limit` | `schema_drift`, with the `endpoint` and a `fingerprint` (a hash of the response's keys and kinds, never values) |
| Anything else (for example a 400 on cycles) | `permanent` |

- Safe pace: one request per second (`WHOOP_API_THROTTLE_MS`); the sidecar enforces it and `describe` declares it. WHOOP's actual limits are not published for this API. `listDeveloperWorkouts` retries 502, 503, 504 and its known 500 up to three times, immediately; no other method retries.
- **Account risk**: this is WHOOP's private app API, used with the owner's own login. It is outside WHOOP's developer terms, may stop working at any time, and an aggressive client could attract rate limiting or account action. Vitamux keeps the connector disabled by default, paces requests and caps the default HR backfill at 90 days. Only connect your own account.
- Nothing from WHOOP is logged: not passwords, MFA codes, sessions, tokens or bodies.

## How Vitamux syncs

The core drives everything through `describe`, `auth/*` and `fetch` ([protocol](../architecture/connectors.md#remote-sidecar-mode)); the sidecar is stateless. One `fetch` call returns **one page**; the core repeats it with the returned cursor until `done`. Correction and backfill send a window (`from`, `to`); an incremental or manual run sends only the stored cursor `{"since": <instant>}` and runs from there (one lookback back for a stream's first run) to now. A page inside a call adds its position (`next`, `next_token`) to the cursor; a finished incremental run stores `{"since": <now>}` and a finished window run leaves the stream cursor alone.

| Stream | Default schedule | Lookback | Unit | Max backfill | Window and cursor |
| --- | --- | --- | --- | --- | --- |
| `whoop.heart_rate` | 1 h | 48 h | 168 h | 90 d | 1-day UTC window (`VITAMUX_WHOOP_HR_WINDOW_H`), step `VITAMUX_WHOOP_HR_STEP_S` (6); cursor `{"next": <window start>}` |
| `whoop.cycles` | 1 h | 72 h | 720 h | 10 y | 7-day UTC window; one raw per window |
| `whoop.sleep` | 1 h | 72 h | 720 h | 10 y | 7-day window of cycles, then one `getSleep` per distinct `sleeps[].activity_id` (naps included); unit `{id, is_nap, timezone_offset}` copied from that sleep; a 404 id is skipped |
| `whoop.workouts` | 1 h | 72 h | 720 h | 10 y | one developer list page (25) per call, cursor `{"next_token"}`, stops when a record is older than `start`; one raw per in-window record and one `weightlifting` call per in-window workout |
| `whoop.strain_deep_dive` | 6 h | 48 h | 720 h | 365 d | one local day per call (owner's timezone); cursor `{"next": <date>}` |

- **Raw**: one NDJSON line per item, `body = {"unit": {...}, "response": <WHOOP's JSON verbatim>}`, `request` holding the endpoint and query, external key `<stream>:<window start>` or `<stream>:<id>`. A workout raw is one record of the list page, sliced verbatim from the page text: key `whoop.workouts:<id>`, unit `{"id"}`, so a new workout never moves the others to other raws (`whoop.workouts:<id>:weightlifting` for detail). The journal (self-reported behaviours) is not offered: it must be off by default, and `vitamux-connector/1` has no per-stream "off by default" yet (an additive field plus core support). Windows sit on a fixed UTC grid and `unit` always spans the full window, so re-fetching an unchanged window yields the same hash (a no-op) and a rescored sleep or recovery inside the lookback yields a new version of the same key. Empty HR and cycles windows emit nothing.
- **Tokens**: an expired access token is refreshed before the call and once after a 401; the new credentials come back in the `result` line for the core to store before anything else.
- **Cost of a page**: a heart-rate day is one request; a sleep page about 8 requests; a workouts page up to 26. At one request per second the core's call timeout for `fetch` must exceed about 30 s.
- **Storage of 6 s heart rate**: 14,400 samples per full day, about 5.3 million per year. As raw JSON that is roughly 0.5 MB per day (about 180 MB per year, estimate) before compression, plus one normalised measurement row per sample. This is why the default backfill stops at 90 days and why the HR stream is the first candidate for the retention and downsampling settings.
- **Normalised in Go** from the raw ([J19.4](../plan/E19-whoop/J19.4-normalizers.md)); resolution rules decide between WHOOP and other sources per metric as usual. Every sleep row is keyed by its `activity_id`, so a rescore supersedes it.
  - **Sessions** are written only by `whoop.sleep`: bounds from the first and last event, stages from the events, nap flag and offset from the unit, totals summed from the stages. The output model cannot attach stages to a session another payload wrote, so the payload that has the stages owns the session. A raw from sidecar 0.2.2 or older has no `is_nap` in its unit: it gives a warning and no rows until the sleep is fetched again.
  - **Cycles** add each sleep's `respiratory_rate` (at its midpoint) and the main sleep's `score` as `whoop_sleep_performance` (at wake-up), plus the daily values `whoop_strain` (`day_strain`), `whoop_recovery`, `resting_heart_rate` and `hrv_rmssd_nightly` (`hrv_rmssd` stored with source unit `s`, converted to ms). Daily values sit at the main sleep's wake-up with its offset, so they share its `sleep_date` ([ADR-0009](../adr/0009-sleep-date-night-window.md)). The **main sleep** is the longest non-nap sleep, else the longest `significant` one; a cycle without one keeps its daily values raw and warns. SpO2, skin temperature, WHOOP's own stage durations, sleep need and the cycle's workouts stay raw.
  - **Strain deep dive** has a normalizer that accepts it and writes nothing, so its payloads read as normalized, not failed.

## Manual real-account checklist

The owner runs this once with their own WHOOP account (J19.1 shape check, J19.7 acceptance). Never paste credentials, MFA codes, tokens, ids or values into issues, logs or docs; report pass or fail and field names only.

1. Start the sidecar with a secret file; confirm `GET /v1/describe` shows the installed upstream version.
2. Connect WHOOP from Vitamux with email and password; complete the MFA step (app, SMS or emailed code). Expect an active connection. Try a wrong code once: expect a retryable refusal, not a crash.
3. Record only the field lists (key paths and value kinds) of one cycle, one sleep, one developer workout, one weightlifting workout, and one strain deep dive, and diff them against this page. Fix any **unverified** item above.
4. Fetch one HR day at 6 s. Note the sample count (about 14,400 expected), whether WHOOP truncates, and the oldest day it still answers for.
5. Check a cycle that spans midnight and a travel day: `days`, `recovery.created_at` and the sleep `timezone_offset` against the WHOOP app.
6. Note the access-token lifetime (`expires_at` minus now, from your own logs of timestamps only) and that a refresh keeps working after a day; note whether the refresh token ever changes.
7. Rescore check: after WHOOP rescoring a recovery or sleep, expect the next sync inside the lookback to create a new raw version, not a duplicate.
8. Sign out of all sessions in WHOOP (or change the password); sync. Expect *needs reauthorization*, then reconnect.
9. Check the sidecar and core logs: no password, code, session, token or body.
