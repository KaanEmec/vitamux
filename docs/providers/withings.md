# Withings

Verified facts about the official Withings Public API (J08.1, checked 2026-10-03), and how Vitamux uses it. The design pattern is in [connectors.md](../architecture/connectors.md#withings-connector-reference-pattern); code is in [`internal/connectors/withings`](../../internal/connectors/withings).

Sources: the [OpenAPI spec](https://developer.withings.com/openapi.yaml) (authoritative for parameters and schemas), the [AI-agent reference](https://developer.withings.com/llms.md), and the Public API guide pages for [authorization URL](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/get-access/oauth-authorization-url), [tokens](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/get-access/access-and-refresh-tokens-no-recover), [notification overview](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/data-api/notifications/notification-overview), [subscribe](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/data-api/notifications/notification-subscribe) and [notification categories](https://developer.withings.com/developer-guide/v3/integration-guide/public-health-data-api/data-api/notifications/notification-content).

## Assumptions checked

| Assumption ([project.md](../architecture/project.md#assumptions-to-verify)) | Result |
| --- | --- |
| Short-lived access tokens | **Confirmed**: 3 h (`expires_in` 10800). |
| Rotating refresh tokens | **Confirmed**: every refresh returns a new refresh token (valid 1 year). The old one stops working 8 h after the new one is issued or as soon as the new access token is used, so a rotated token must be persisted before use (the runtime's single-flight refresh does). |
| `getmeas` `lastupdate` and paging | **Confirmed**: `lastupdate` returns groups created *or modified* after it and must not be combined with `startdate`/`enddate`. Responses carry `more` and `offset`; repeat the same call with `offset` until `more` is 0. |
| meastype codes | **Confirmed** for every code in the `W` column of [metric-catalog.md](../architecture/metric-catalog.md) and [metrics.md](../metrics.md). Vascular age is 155 in the OpenAPI spec (the AI-agent page says 140; the spec wins). |
| `value×10^unit` | **Confirmed** (`unit` is the power of ten). Exception: 130 and 139 are classification integers, not quantities. |
| Notify `appli` codes | **Corrected**: they were unspecified in our docs, and the AI-agent page lists wrong values (16 for BP, 4 for sleep). The category page is authoritative: see [notifications](#notifications). |
| HEAD-validated callbacks | **Corrected in scope**: Withings sends `HEAD` to the *notification* callback when subscribing (200 required, 204 refused). The dashboard's *Test* button probes the registered OAuth URL and accepts only 200 (it reports 204 as "couldn't reach"), so Vitamux answers `HEAD /oauth/withings/callback` with an empty 200 that never consumes a state. |
| Rate limits | **Confirmed with numbers**: 120 requests per minute per application on the standard plan; poll a user at most every 10 minutes. HTTP 429 or body status 601 means too many requests. |

## App registration and callback

- Each self-hoster creates their own application in the [developer dashboard](https://developer.withings.com/dashboard/) (Public API, no contract, EU cloud `https://wbsapi.withings.net`). It yields the client id and secret, entered in the panel (`PUT /api/v1/providers/withings/app-credentials`, sealed, used from the next authorization on) or, overriding it, as `VITAMUX_WITHINGS_CLIENT_ID` and `VITAMUX_WITHINGS_CLIENT_SECRET_FILE` (the plain `VITAMUX_WITHINGS_CLIENT_SECRET` only in development) ([ADR-0021](../adr/0021-source-setup.md)).
- Verify checks them without a user grant: `POST https://wbsapi.withings.net/v2/signature` with `action=getnonce`, `client_id`, `timestamp` and `signature` = hex HMAC-SHA256 under the client secret of `getnonce,<client_id>,<timestamp>` (the values sorted by key, comma-joined). Status 0 means accepted; Vitamux does not use the nonce. A synthetic client id was answered with a refusal (checked 2026-10-04).
- Register `${VITAMUX_PUBLIC_URL}/oauth/withings/callback` as the application's callback URL. `redirect_uri` must match a registered URL; several may be registered, comma-separated.
- Production callbacks (OAuth and notification) need HTTPS, a domain name (no IP or localhost), port 80 or 443, at most 255 characters. Without that the application runs in restricted mode, limited to 10 linked users: fine for development.

## OAuth 2.0

| Step | Request | Notes |
| --- | --- | --- |
| Authorize | `GET https://account.withings.com/oauth2_user/authorize2?response_type=code&client_id&scope&redirect_uri&state` | Vitamux asks for `user.metrics` (getmeas and notify) and `user.activity` (activity, intraday and sleep). A connection authorized before the activity streams lacks `user.activity`: reconnect it once. `mode=demo` uses the demo user. |
| Callback | `redirect_uri?code=…&state=…` | The code is valid **30 seconds**: exchange at once. |
| Exchange | `POST https://wbsapi.withings.net/v2/oauth2` form `action=requesttoken&grant_type=authorization_code&client_id&client_secret&code&redirect_uri` | Body has `userid`, `access_token`, `refresh_token`, `expires_in`, `scope`, `token_type`. Keep `userid`: Vitamux stores only its SHA-256 as `account_key`. `userid` is an integer in the spec and a string in examples; both are accepted. |
| Refresh | same endpoint, `grant_type=refresh_token&refresh_token` | Always store the new refresh token. |
| Revoke | `action=revoke` on `/v2/oauth2` | Needs a signed request (nonce + HMAC-SHA256); not used yet. Disconnect deletes the local credentials; the owner can revoke the app in the Withings account. |

Every API call is a form POST with `Authorization: Bearer <access_token>`. Responses are HTTP 200 with `{"status": N, "body": {...}}`; `status` 0 is success. Mapping to [typed errors](../architecture/connectors.md#typed-errors): 401, 342, 343 (and a refused refresh token: HTTP 400/401, status 503 or `invalid_grant`) → reauth required; 601 or HTTP 429 → rate limited; 2554, 2555 and HTTP 5xx → transient; other statuses → permanent.

## Measures (`getmeas`)

`POST https://wbsapi.withings.net/measure`, `action=getmeas`, optional `meastype` or `meastypes`, `category` (1 real measures, 2 user objectives), `startdate`/`enddate` (unix seconds, filter on measurement `date`) **or** `lastupdate`, and `offset`.

Response body: `updatetime` (server time of the answer; the next `lastupdate`), `timezone` (the account's zone, one per response), `measuregrps`, `more`, `offset`. A measure group has `grpid` (int64), `attrib`, `date`, `created`, `modified`, `category`, `deviceid`, `hash_deviceid`, `model`, the model id, `comment` (deprecated, empty) and `measures[]` of `{value, type, unit}` (`algo`, `fm` deprecated; segmental types add `position`). The model id is `modelid` in live responses and `model_id` in the spec (checked 2026-10-04); device fields are all null on manual entries. Creating, updating or deleting a measure updates its group.

`attrib`: 0 and 8 device, unambiguous · 1 device, may belong to another user · 2 entered manually · 4 entered manually at account creation · 5 BPM auto (best of several) · 7 confirmed activity · 15 guided conditions (Nerve Health Score).

Open: how a *deleted* group appears in a `lastupdate` response is not documented. The connector does not infer deletions from absence; a deleted group stays until reprocessed by hand.

### How Vitamux syncs

- Stream `withings.measures`: incremental every hour on `lastupdate` (first run from 0, i.e. the whole history), cursor `{"lastupdate": updatetime}` advanced after the last page; a daily correction re-fetches the last 7 days by `startdate`/`enddate`; backfill units of 30 days by date. All requests send `category=1`. Rate limit 120/min.
- Raw: one record per measure group, external key `measuregrp:<grpid>`, body `{"timezone": <response timezone>, "measuregrp": <group as received>}`, so an unchanged group is a no-op and a modified one a new raw version.
- Normalizer (`withings.measures` v3): groups with 9/10 → `bp_reading` (11 → `bp_pulse`), groups with body-composition types → `body_composition`, every other type a plain sample (11 outside BP → `heart_rate`). `attrib` 2 and 4 set `manual_entry`. Device fingerprint is `hash_deviceid` (else `deviceid`), typed as in [devices](#devices). Local dates come from the owner's timezone periods, not from the response `timezone`, which is the account's current zone rather than the zone of each measurement. Unknown types → warning `unknown_meastype`, raw kept. Category 2 groups are skipped with a warning.
- Measure types beyond the catalogue's W column: 12 is a generic temperature (room temperature on WS-50 and Home), so it is `body_temperature` only from a thermometer, else warning `not_body_temperature`. 135–138 are the ECG intervals QRS, PR, QT and QTc in seconds; 167 and 196 the nerve health and nerve response scores; 227 metabolic age; 229 electrochemical skin conductance (µS). 173, 174 and 175 (fat-free, fat and muscle mass) are segmental: `position` 2 right arm, 3 left arm, 10 left leg, 11 right leg, 12 trunk names the code (`fat_mass_left_leg`, …), so the duplicate check covers the position; another position → warning `unknown_position`. 130 (from ECG) and 139 (from PPG) are AFib classifications: the events `afib_ecg_result` and `afib_ppg_result`, with the category word as level (0 negative, 1 positive, 2 inconclusive, 3 no signal, 4 other, 5 noise, 6 low heart rate, 7 high heart rate, 8 inconclusive US, 9 to 12 negative or positive with normal or high HR, 13 no diagnosis) and `{"category": N}` as context; a category outside 0 to 13 → warning `unknown_afib_category`. The U-Scan types (names and units from the OpenAPI spec) are `urine_ph` (147), `urine_specific_gravity` (148), `urine_nitrites` (151, µmol/L), `urine_ketones` (204), `urine_vitamin_c` (205), `urine_calcium` (248), `urine_creatinine` (249) (all mmol/L) and `urine_calcium_creatinine_ratio` (251, mmol/mmol).

### Devices

Model id → device type: 1–7, 9–12, 14–16, 18 `scale`; 13 (Sleep Analyzer) and 60–63 `under_mattress`; 41–48 `bp_monitor` (45 BPM Connect); 51, 54, 58 `band`; 52, 53, 55, 59, 90–95 `watch`; 70, 71 `thermometer`. Without a model id, a word of the model name decides (`BPM`, `Sleep`, `Thermo`, `ScanWatch`, `Body`). 1051–1060 are phone apps relaying into Withings: their records carry the origin `relay:model:<id>` instead of a device type, and `known_relay_origins` (`relay:%` → `phone_app`) flags it relayed, so they are never counted twice. Activity rows of `brand` 18 are relays too (`relay:brand:18`).

## Activity, intraday and sleep

Field names checked 2026-10-04. All three are form POSTs with `data_fields` listing the fields Vitamux reads.

| Stream | Call | Sync | Raw record |
| --- | --- | --- | --- |
| `withings.activity` | `POST /v2/measure`, `action=getactivity` | `lastupdate` hourly with `offset`/`more` paging; correction (7 days) and backfill (30-day units) by `startdateymd`/`enddateymd` | `activity:<date>:<brand>:<device>`, `{"activity": <as received>}` |
| `withings.intraday` | `POST /v2/measure`, `action=getintradayactivity` | `startdate`/`enddate`, at most 24 h a call; whole hours from the stored start (first sync: the last 7 days) to the slot, then the next run starts 6 h earlier (late uploads); correction 2 days; backfill in 1-day units | `intraday:<hour>`, `{"entries": [{"timestamp", "data": <entry as received>}]}`, only entries inside the call's window, so an hour is always whole |
| `withings.sleep` | `POST /v2/sleep`, `action=getsummary`, then `action=get` over each night (≤ 24 h) | as activity | `sleep:<id>`, `{"summary": …, "states": […]}` |

getactivity and getsummary answer without an `updatetime`, so the next `lastupdate` is the run's slot minus one hour; re-fetched records are no-ops. An empty intraday `series` is `[]`.

Normalizers (version 1 each, intraday 2; local days from each record's IANA `timezone`):

- **Activity** → daily values: `steps`, `distance` → `distance_walk_run`, `elevation` (floors climbed) → `floors_climbed`, `calories` (active) → `active_energy`, `totalcalories` → `total_energy`, `soft`/`moderate`/`intense` → `intensity_light_time`/`intensity_moderate_time`/`intensity_vigorous_time`. Heart-rate summary and zones stay raw. The device carries no model, so it is typed by the intraday or measures records of the same fingerprint (a watch's steps then take part in the steps built-in). `brand` 18 rows are relays (`relay:brand:18`).
- **Intraday** → `steps`, `elevation`, `calories`, `distance`, `stroke` (`swim_strokes`) as intervals `[t, t + duration)`; `heart_rate`, `spo2_auto` (`spo2`), `rr` (`respiratory_rate`), `rmssd` (`hrv_rmssd`, few-second window) and `sdnn1` (`hrv_sdnn`, 1-minute window) as samples. An interval field without `duration` → warning `intraday_without_duration`. `core_body_temperature` (°C, an estimate) → `core_body_temperature_estimated`, never `body_temperature`.
- **Sleep** → one session per night with stages: state 0 awake, 1 light, 2 deep, 3 rem, 4 (manual) and 5 `asleep_unspecified`, 15 `out_of_bed`; others → warning `unknown_sleep_state`. States of another model id in the same window are skipped. Totals from `total_sleep_time`, `deepsleepduration`, `lightsleepduration`, `remsleepduration`, `wakeupduration` and `sleep_latency`. Daily values of the night's `date`: `wakeupcount` → `sleep_awakenings`, `snoring` → `sleep_snoring_time`, `snoringepisodecount` → `sleep_snoring_episodes`, `apnea_hypopnea_index`, `sleep_score` → `withings_sleep_score`, `breathing_disturbances_intensity` → `withings_breathing_quality`, `hr_average` → `sleeping_heart_rate`, `rr_average` → `respiratory_rate_nightly`. `sleep_efficiency` (a ratio) and `waso` stay raw: the engine derives `sleep_efficiency` and `sleep_waso` from the session. `hr_min` is not resting heart rate. Sessions carry no flags, so state 4's manual origin is visible in raw only.

## Notifications

- `POST https://wbsapi.withings.net/notify` with `action=subscribe|list|get|update|revoke`, `callbackurl`, `appli`; one subscription per user and `appli`.
- At subscribe time Withings sends `HEAD` to the callback URL and expects 200 (204 is refused). Notifications are form POSTs (`userid`, `appli`, `startdate`, `enddate`, or `date`/`deviceid` for events) and must get HTTP 200 within a few seconds. Failures are retried 5 cycles over about 5 hours (2 attempts each, with jitter); persistent failure leads to warning emails and, after 20 days, cancellation.
- `appli` values for measures: **1** weight and body composition, **2** temperature, **4** blood pressure, heart rate and SpO2. Others (acknowledged and ignored; polling covers them): 16 activity, 44 sleep, 46 profile change, 50–52 bed events, 54 ECG, 55 ECG failed, 58 glucose, 61 stethoscope, 62 HRV, 63 urine (U-Scan).

### How Vitamux uses notifications

- Optional per install: owner setting `withings.notifications` (default off; `withings.Notifications.SetEnabled`). Polling runs either way; notifications only lower latency. Turning it on needs `VITAMUX_PUBLIC_URL`.
- On: each active connection (and every later connect or reauthorization) gets applis 1, 2 and 4 on one callback `${VITAMUX_PUBLIC_URL}/webhooks/withings/<hook_token>`. The token is 32 random bytes; only its SHA-256 is stored (`connections.hook_token_hash`) and the token is only ever sent to Withings. Subscribing lists the profiles first, so repeating it is safe; Vitamux callbacks with older tokens are revoked.
- Off: every Vitamux profile is revoked and the hash cleared, so later notifications get 404 (Withings cancels a callback that keeps failing).
- `HEAD`/`GET` on the callback answer an empty 200. A `POST` with an unknown token is 404 and enqueues nothing. A measures notification (appli 1, 2, 4) whose `userid` matches the connection enqueues one correction sync of `[startdate, enddate]` (by measurement date; the cursor stays), deduplicated per connection, stream and window while it is queued or running. Other categories, users or windows longer than 31 days are acknowledged (200) and ignored. A notification that arrives while the same window is already running is folded into it; the next `lastupdate` poll catches anything it missed.

## Fixtures

`go run ./tools/fixturegen` also writes `withings/getmeas-NNNN.json` (synthetic getmeas pages of the Withings BP cuff and scale groups with the live `modelid`, oldest first, 100 groups per page with `more`/`offset`) and `withings/getmeas-corrections.json` (the corrected group versions a later `lastupdate` call returns). Deleted groups are not rendered (see the open point above).

## Manual real-account checklist

The owner runs this once against their own Withings account and BP monitor (E08 acceptance). Never paste real credentials, user ids, tokens, hook URLs or readings into issues, logs or docs; report only pass or fail per step.

1. Register a Withings application (see [app registration](#app-registration-and-callback)) with `${VITAMUX_PUBLIC_URL}/oauth/withings/callback`; set `VITAMUX_WITHINGS_CLIENT_ID` and `VITAMUX_WITHINGS_CLIENT_SECRET_FILE`; start Vitamux.
2. Connections › Withings › Connect; consent at Withings. Expect a redirect to `/connections?connected=withings` and an active connection.
3. Wait for the first sync (or trigger a sync). Expect your BP readings under source data, each with systolic, diastolic and pulse in one reading, local dates in your timezone, and manual entries flagged.
4. Take a new BP reading; trigger a sync (or wait up to an hour). Expect exactly one new reading.
5. Edit a reading in the Withings app (if it allows); sync. Expect the edited values to supersede the old ones (history keeps both).
6. Optional, needs a public HTTPS domain: turn `withings.notifications` on. Take a reading; expect it within a minute without a manual sync. Turn it off; expect the subscriptions gone (the notify list in the Withings developer dashboard, or no more callbacks in the access log).
7. Leave Vitamux running for more than 3 hours; sync. Expect success (the access token was refreshed and the rotated refresh token kept).
8. Revoke the application in your Withings account settings; sync. Expect the connection to show *needs reauthorization*. Reconnect; expect it active again and the readings taken meanwhile imported once.
9. Check the logs: no tokens, codes, user ids, hook URLs or values.
