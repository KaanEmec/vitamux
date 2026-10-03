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
| HEAD-validated callbacks | **Corrected in scope**: Withings sends `HEAD` to the *notification* callback when subscribing (2xx required). Nothing documents a HEAD on the OAuth redirect URI; Vitamux still answers `HEAD /oauth/withings/callback` with 204 because a probe must never consume a state. |
| Rate limits | **Confirmed with numbers**: 120 requests per minute per application on the standard plan; poll a user at most every 10 minutes. HTTP 429 or body status 601 means too many requests. |

## App registration and callback

- Each self-hoster creates their own application in the [developer dashboard](https://developer.withings.com/dashboard/) (Public API, no contract, EU cloud `https://wbsapi.withings.net`). It yields the client id and secret: `VITAMUX_WITHINGS_CLIENT_ID` and `VITAMUX_WITHINGS_CLIENT_SECRET_FILE` (the plain `VITAMUX_WITHINGS_CLIENT_SECRET` only in development).
- Register `${VITAMUX_PUBLIC_URL}/oauth/withings/callback` as the application's callback URL. `redirect_uri` must match a registered URL; several may be registered, comma-separated.
- Production callbacks (OAuth and notification) need HTTPS, a domain name (no IP or localhost), port 80 or 443, at most 255 characters. Without that the application runs in restricted mode, limited to 10 linked users: fine for development.

## OAuth 2.0

| Step | Request | Notes |
| --- | --- | --- |
| Authorize | `GET https://account.withings.com/oauth2_user/authorize2?response_type=code&client_id&scope&redirect_uri&state` | Vitamux asks for `user.metrics` (getmeas and notify). `mode=demo` uses the demo user. |
| Callback | `redirect_uri?code=…&state=…` | The code is valid **30 seconds**: exchange at once. |
| Exchange | `POST https://wbsapi.withings.net/v2/oauth2` form `action=requesttoken&grant_type=authorization_code&client_id&client_secret&code&redirect_uri` | Body has `userid`, `access_token`, `refresh_token`, `expires_in`, `scope`, `token_type`. Keep `userid`: Vitamux stores only its SHA-256 as `account_key`. `userid` is an integer in the spec and a string in examples; both are accepted. |
| Refresh | same endpoint, `grant_type=refresh_token&refresh_token` | Always store the new refresh token. |
| Revoke | `action=revoke` on `/v2/oauth2` | Needs a signed request (nonce + HMAC-SHA256); not used yet. Disconnect deletes the local credentials; the owner can revoke the app in the Withings account. |

Every API call is a form POST with `Authorization: Bearer <access_token>`. Responses are HTTP 200 with `{"status": N, "body": {...}}`; `status` 0 is success. Mapping to [typed errors](../architecture/connectors.md#typed-errors): 401, 342, 343 (and a refused refresh token: HTTP 400/401, status 503 or `invalid_grant`) → reauth required; 601 or HTTP 429 → rate limited; 2554, 2555 and HTTP 5xx → transient; other statuses → permanent.

## Measures (`getmeas`)

`POST https://wbsapi.withings.net/measure`, `action=getmeas`, optional `meastype` or `meastypes`, `category` (1 real measures, 2 user objectives), `startdate`/`enddate` (unix seconds, filter on measurement `date`) **or** `lastupdate`, and `offset`.

Response body: `updatetime` (server time of the answer; the next `lastupdate`), `timezone` (the account's zone, one per response), `measuregrps`, `more`, `offset`. A measure group has `grpid` (int64), `attrib`, `date`, `created`, `modified`, `category`, `deviceid`, `hash_deviceid`, `model`, `model_id`, `comment` (deprecated, empty) and `measures[]` of `{value, type, unit}` (`algo`, `fm` deprecated). Creating, updating or deleting a measure updates its group.

`attrib`: 0 and 8 device, unambiguous · 1 device, may belong to another user · 2 entered manually · 4 entered manually at account creation · 5 BPM auto (best of several) · 7 confirmed activity · 15 guided conditions (Nerve Health Score).

Open: how a *deleted* group appears in a `lastupdate` response is not documented. The connector does not infer deletions from absence; a deleted group stays until reprocessed by hand.

### How Vitamux syncs

- Stream `withings.measures`: incremental every hour on `lastupdate` (first run from 0, i.e. the whole history), cursor `{"lastupdate": updatetime}` advanced after the last page; a daily correction re-fetches the last 7 days by `startdate`/`enddate`; backfill units of 30 days by date. All requests send `category=1`. Rate limit 120/min.
- Raw: one record per measure group, external key `measuregrp:<grpid>`, body `{"timezone": <response timezone>, "measuregrp": <group as received>}`, so an unchanged group is a no-op and a modified one a new raw version.
- Normalizer: groups with 9/10 → `bp_reading` (11 → `bp_pulse`), groups with body-composition types → `body_composition`, every other type a plain sample (11 outside BP → `heart_rate`). `attrib` 2 and 4 set `manual_entry`. Device fingerprint is `hash_deviceid` (else `deviceid`). Local dates come from the owner's timezone periods, not from the response `timezone`, which is the account's current zone rather than the zone of each measurement. Unknown types → warning `unknown_meastype`, raw kept. Category 2 groups are skipped with a warning.

## Notifications

- `POST https://wbsapi.withings.net/notify` with `action=subscribe|list|get|update|revoke`, `callbackurl`, `appli`; one subscription per user and `appli`.
- At subscribe time Withings sends `HEAD` to the callback URL and expects 2xx. Notifications are form POSTs (`userid`, `appli`, `startdate`, `enddate`, or `date`/`deviceid` for events) and must get HTTP 2xx within a few seconds. Failures are retried 5 cycles over about 5 hours (2 attempts each, with jitter); persistent failure leads to warning emails and, after 20 days, cancellation.
- `appli` values for measures: **1** weight and body composition, **2** temperature, **4** blood pressure, heart rate and SpO2. Others: 16 activity, 44 sleep, 46 profile change, 50–52 bed events, 54 ECG, 55 ECG failed, 58 glucose, 61 stethoscope, 62 HRV, 63 urine (U-Scan).

## Fixtures

`go run ./tools/fixturegen` also writes `withings/getmeas-NNNN.json` (synthetic getmeas pages of the Withings BP cuff and scale groups, oldest first, 100 groups per page with `more`/`offset`) and `withings/getmeas-corrections.json` (the corrected group versions a later `lastupdate` call returns). Deleted groups are not rendered (see the open point above).
