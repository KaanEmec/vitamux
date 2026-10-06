# Vitamux iOS app

This document is for epic [E22](../plan/E22-ios-app/README.md). Its structural decisions are recorded in [ADR-0023](../adr/0023-ios-app.md) (proposed until the owner accepts it); the Apple Watch data contract is [ADR-0024](../adr/0024-watch-data.md) ([J22.15](../plan/E22-ios-app/J22.15-watch-data-contract.md)), also proposed. Screens and deep links: [ios-app-screens](ios-app-screens.md). Visual design is deliberately out of the first pass: screens use stock SwiftUI with the panel's data-status and source cues, and a design pass follows parity ([J22.24](../plan/E22-ios-app/J22.24-design-pass.md)).

## Goal and scope

A native iPhone app that is a full client of a self-hosted Vitamux server and the device that syncs Apple Health. It:

- connects to a server URL the owner types (or scans), and signs in with username, password and TOTP like the panel;
- offers every feature of the web panel ([parity matrix](#parity-matrix)), with metric visualisation and Apple Health first;
- replaces the Vitamux Bridge app: the HealthKit sync from [E15](../plan/E15-apple-health/README.md) runs inside it, through the unchanged [HealthBridgeKit](apple-health.md#structure);
- brings in everything Apple Watch records, through the iPhone's Health store ([Apple Watch](#apple-watch));
- adds what a phone does better: home-screen widgets, an offline read cache, local notifications, and lab PDFs from the camera or Files.

Owner decisions (2026-10-04):

- Replace Bridge; HealthBridgeKit stays a reusable package.
- Bearer app sessions after username, password and TOTP.
- Build from source with the owner's paid Apple team, as for Bridge ([distribution](apple-health.md#distribution)). App Store and TestFlight stay deferred, but the app stays review-ready.
- Full parity, including admin and ops settings.
- iOS 18 minimum, iPhone only, portrait-first.
- Ships as v0.4.0 after E20, so the source-setup screens mirror the finished guided setup.
- Apple Watch: every type the Watch records, read on the iPhone, sensitive groups included but off until turned on. No watchOS app.
- A lean, clean architecture that is easy to manage ([lean architecture](#lean-architecture)).

Not in scope: iPad layouts, a watchOS app, HealthKit write-back, APNs push, several servers signed in at once, a second user, anything the panel does not do (scores, goals, advice).

## Structure

| Part | Contents |
| --- | --- |
| `apple/HealthBridgeKit` (existing package) | Unchanged role: Core and HealthKit sync. J22.14 needed no additions (the app keeps the status snapshot). Still usable by a third-party app. |
| `apple/VitamuxKit` (new package) | One library target, three folders: `API/` (client generated from `api/openapi.yaml`, one middleware), `Core/` (server profile, session store, `Problem`, `Loadable`, poller, file cache), `Charts/` (Swift Charts views and the chart grammar). One test target, run with `swift test` on macOS, no simulator needed. |
| `apple/VitamuxApp` (xcodegen project) | One app target with a folder per feature, the widget extension and UI tests. Replaced `apple/HealthBridgeApp` (removed in J22.14). |
| Backend | App sessions and the native connection-auth return ([J22.2](../plan/E22-ios-app/J22.2-app-sessions.md), [J22.3](../plan/E22-ios-app/J22.3-native-auth-return.md)). No other iOS coupling. |

## Lean architecture

The app is plain SwiftUI. A contributor who knows SwiftUI should find any screen in a minute and change it without learning a framework.

- **Two packages, one app target.** HealthBridgeKit (sync) and VitamuxKit (talking to the server, drawing charts). Code goes in a package only when it has no UI state or when the widget also needs it; everything else lives in the app.
- **A feature is a folder.** `Dashboard/`, `Explore/`, `Rules/`, `Sources/`, `Lab/`, `Settings/`, `AppleHealth/`: a few views and, where a screen loads or changes data, one `@Observable` model that calls the generated client directly. No repositories, use-case layers, coordinators, view-model protocols or DI containers.
- **One shared state object.** `AppState` in the environment holds the server profile, the session, the client and the router. Screens get what they need from it and keep their own loading and error state through one `Loadable<Value>` and one `ProblemView`.
- **One navigation model.** A `NavigationStack` per tab and one `Route` enum. Deep links, widgets, notifications and the OAuth return all parse into a `Route`.
- **Generated, not written.** The API types and calls come from the spec through Apple's swift-openapi-generator build plugin, reading a copy of `api/openapi.yaml` that `make openapi` writes into the package (`copy-openapi.sh` rewrites nullable `$ref`s the generator drops; CI fails if the copy drifts). Nothing generated is committed.
- **Few dependencies.** Apple frameworks and Apple's swift-openapi packages (generator, runtime, URLSession transport) only, plus the packages they resolve themselves (J22.4 pins that list). No analytics, UI kits, Combine pipelines, Core Data or SwiftData; the cache is files. A CI check fails on any other package in `Package.resolved`.
- **Extract on second use.** A shared component appears when a second screen needs it, as in the panel. No speculative abstractions, feature flags or plugin points.
- **Swift 6 strict concurrency**, `async`/`await` only. The app target is main-actor by default, so models are main-actor without annotations; VitamuxKit stays `nonisolated` so the widget can use it.
- **Copy the worked example.** A new screen starts as a copy of the [worked example](../../apple/VitamuxApp/README.md#worked-example): a view, its model, a `Route` case and a UI test.

## Server and sign-in

- **Server profile:** a base URL, `https` only. A debug build also accepts `http` for `localhost`, `*.local` and private addresses, matching `VITAMUX_ENV=development`. The app reads `GET /api/v1/system/version` before sign-in and refuses a server older than its minimum API version with a plain message. QR: the pairing QR (`{"url","code"}`) also fills the URL.
- **App sessions:** `POST /api/v1/auth/login` with `{"client": "app", "device_name": …}` answers a bearer token `vmx_ses_<id>_<secret>` in the body instead of a cookie. It is a row in the same sessions table with `kind = app` and a name, so Settings › Security lists and ends it like a browser session. Bearer requests need no CSRF token. Lifetime: 30 days idle, 90 days absolute, configurable (`VITAMUX_APP_SESSION_*`). Login throttling, TOTP and recovery codes behave exactly as for the panel, and the login API stays stateless between the password and code steps.
- **Session-only endpoints** (auth, TOTP, password, sessions, connection auth) admit an app session as a `session` principal; API keys still never reach them. `api/authz.yaml` and `TestAuthzMatrixEnforced` gain the app-session caller.
- **Storage:** the token sits in the Keychain (`AfterFirstUnlockThisDeviceOnly`, shared with the widget through an access group) so background refresh works while locked. Optional Face ID or passcode app lock (LocalAuthentication); the app-switcher snapshot is blurred.
- **Expiry:** any `401` other than on login clears the session and returns to sign-in with "session expired", keeping the server URL. Sign-out revokes the app session. It does **not** stop Apple Health sync, which uses its own device token; the sign-out sheet offers "also unpair this iPhone".

## Connecting sources from the phone

- **Prompt steps** (Garmin and WHOOP sign-in with MFA) are plain JSON: `auth/begin` and `auth/continue` work over the bearer session. The `vitamux_oauth` binding cookie lives in the app's `URLSession` cookie store, so it returns on `continue`.
- **OAuth redirects** (Withings) run in `ASWebAuthenticationSession`, whose cookie store is not the app's. Decided ([J22.3](../plan/E22-ios-app/J22.3-native-auth-return.md)): `auth/begin` with `{"return": "app"}` answers a `redirect_url` on the Vitamux server, `/oauth/{provider}/start?ticket=…`, that sets the binding cookie inside the auth browser and redirects to the provider. The callback, for a state marked as app-originated, redirects to `vitamux://connections?connected=<provider>` or `?auth_error=<code>` instead of `/connections`. The scheme is fixed, so this is not an open redirect. The browser flow is unchanged.
- **Guided setup** (E20): the same setup states, Withings app-credential wizard (write-only secrets, callback URL shown with a copy button) and "sidecar not running" card.

## API client

- The generator plugin reads `VitamuxKit/Sources/VitamuxKit/API/openapi.yaml`, a copy that `make openapi` refreshes from `api/openapi.yaml` (a symlink was tried first; the generator silently drops nullable `$ref` fields, so the copy step rewrites them). CI fails when the copy drifts.
- One middleware adds `Authorization: Bearer`, maps `application/problem+json` to `Problem` (title, detail, field `pointer`s), turns `429` into a wait with `Retry-After`, and sends `401` to sign-in.
- Polling copies the panel's intervals (backfills 5 s, extraction 2 s, export 1 s), only while the screen is visible.
- A fake server (`URLProtocol` stub with synthetic JSON, the counterpart of `web/e2e/fake-api.ts`) serves unit tests, and the app's debug build under `-uitest` for UI tests, as Bridge does today.

## Navigation

Tab bar: **Dashboard · Explore · Sources · Lab · More**. More holds Rules, Settings and Apple Health; Sources opens with a "This iPhone" card for Apple Health. Global search (the ⌘K palette's job) is a search field over sections, settings pages, connections, metrics and their rules. Deep links use `vitamux://` and match panel paths (`vitamux://explore/heart_rate_resting?range=3M`), so widgets, notifications and the OAuth return share one router. Every screen, sheet and link: [ios-app-screens](ios-app-screens.md).

## Parity matrix

Every panel route and every owner endpoint the panel calls has an app screen. "Same" means the same behaviour, endpoints and confirmations.

| Panel | App | Notes | Job |
| --- | --- | --- | --- |
| Login (password, TOTP or recovery code, rate limit, expired) | Server + sign-in | App session; Face ID lock | J22.5 |
| Shell: nav, ⌘K palette, sync pill, theme, sign-out | Tab bar, search, sync status, theme, sign-out | Theme is an app preference | J22.5 |
| Dashboard: day picker, alerts, cards, edit mode, add metric, sources | Same | Reorder by drag or move up/down | J22.7 |
| Explore inventory, filters, pins | Same | | J22.8 |
| Metric detail: range, brush, baseline, sources, coverage, stats, values table | Same | Pinch or drag to zoom | J22.8 |
| PointPanel, overrides (exclude, force, set value), provenance | Same | Sheet | J22.8 |
| All-sources day view, overrides list, revoke | Same | | J22.8 |
| Sleep, blood pressure, body composition, workouts, events, lab analyte views | Same | | J22.9 |
| Rules catalogue, coverage heatmap, rule page, versions, diff, activate | Same | | J22.10 |
| Rule lens (draft overlay, save, activate, revert) | Bottom sheet over the chart | | J22.10 |
| Rule builder (5 steps, live preview, field errors) | Same, one step per screen | | J22.10 |
| Connections list, run strip, banners | Sources | | J22.11 |
| Connection detail: overview, streams, devices (type, name, merge), backfills, history, settings and schedules, delete | Same | | J22.11 |
| Connect wizard: OAuth, prompt steps, E20 setup states and wizards | Same | `ASWebAuthenticationSession` | J22.11 |
| Lab documents: upload, extract with consent, delete | Same, plus camera scan, Files, share sheet | | J22.12 |
| Lab review: PDF with row outline, row editor, accept/reject, confirm, unconfirm | Same | PDFKit, no pdf.js | J22.12 |
| Lab results by analyte, history | Same | | J22.12 |
| Settings › Profile, timezone periods, Withings notifications | Same | | J22.13 |
| Settings › Devices: pairing code, devices, resync, revoke, origins | Same, plus "This iPhone" | QR shown for other phones | J22.13, J22.14 |
| Apple Health source filter (take or ignore per app and type) | Apple Health › Sources | New in both clients | J22.25 |
| Intraday Day view with zoom to buckets and raw samples | Day range on metric detail | New in both clients | J22.26 |
| Settings › AI providers, API keys (shown once), Security (password, TOTP, sessions) | Same | TOTP: `otpauth://` link and copy | J22.13 |
| Settings › Sources: source order, provider apps (write-only secrets, verify), sidecars (add, remove, secret shown once) | Same | Reuses J22.11's app wizard | J22.13 |
| Settings › Retention, Backups and export, System status | Same | Export saved to Files | J22.13 |
| Bridge app: pairing, groups, per-type status, sync now, anchor reset, privacy | Apple Health | Pairing is one tap when signed in | J22.14 |

New in both clients: the Apple Watch views ([J22.18](../plan/E22-ios-app/J22.18-watch-views.md)).

Checked against `web/src/routes` and `api/authz.yaml` (2026-10-05). Out of scope, with the reason:

- `/data/*`: redirects that keep old panel bookmarks working; the app has no old links.
- Operations the panel does not call: `POST /measurements/manual`, `POST /connections` (push connections), `POST`/`DELETE /analytes/aliases`, `GET /event-types`, `GET /workouts`, `GET /workouts/{id}` and `GET /sleep/{id}` (the panel uses the resolved views). They join when a panel job adds them.
- `/api/ingest/v1/*`: the device API, called by HealthBridgeKit, not by screens (J22.14).
- `GET /oauth/{provider}/callback` and `/webhooks/withings/*`: reached by the provider, not by a client.

## Charts

- **Swift Charts**, iOS 18 vectorized plots (`LinePlot`, `AreaPlot`) for long series. Above about 2,000 points per series the kit decimates to min/max per pixel column, so a 14,400-point day stays smooth.
- **Grammar:** `chartFor(metric)` in VitamuxKit's `Charts/` mirrors [the panel's](frontend.md#chart-grammar), driven by `GET /metrics`. A shared fixture of catalogue entries and expected views is tested by both `web` and VitamuxKit, so the two cannot drift.
- **Views:** line with band, bars, step, line with baseline, stage stack, hypnogram, dumbbell, event lanes, lab points with the printed range, sparkline, coverage strip; for Watch data also an ECG strip, a beat-to-beat (RR) plot, activity rings and a workout route ([Apple Watch](#apple-watch)). Each has a range picker, selection with a callout, status markers (shape, colour and word), source series that differ by dash as well as colour, and a draft ghost series for the rule lens.
- **Intraday:** a metric's `intraday` catalogue metadata gives its default and finest bucket; the Day view zooms through the ladder (heart rate 1 min → 30 s → raw, steps 30 → 15 → 5 min) with night and workout overlays ([J22.26](../plan/E22-ios-app/J22.26-intraday-views.md)). Metrics measured once a day have no Day view.
- **Accessibility:** chart descriptors for VoiceOver and Audio Graphs, and a "Show as table" fallback on every chart.
- **Copy:** neutral words as in [the design system](frontend.md#design-system): "30-day mean", "as printed", no good or bad.

## Apple Health

The app embeds HealthBridgeKit and owns the HealthKit entitlements, purpose string, observer registration in `didFinishLaunching`, background delivery and `BGAppRefreshTask` exactly as [described for E15](apple-health.md#sync-algorithm). Changes:

- **One-tap pairing:** signed in, the app creates a pairing code (`POST /devices/pairing-codes`) and redeems it itself (`POST /api/ingest/v1/devices/pair`). The device token stays separate from the app session, with its own Keychain item, so ingest keeps working after sign-out and a stolen session cannot impersonate the device. Manual QR pairing remains for a phone that only syncs. Code creation for an app caller must not need `VITAMUX_PUBLIC_URL`, since no QR is shown (J22.3).
- **Source filter (take or ignore):** the owner chooses per app seen in Apple Health, and optionally per type, whether Vitamux takes its data or ignores it. A provider connected directly (WHOOP, Garmin) is ignored by default with the reason shown. The filter is stored with the device on the server, applied on the phone as a HealthKit source predicate so ignored data never leaves it, editable from the app and the panel, and backed by a server guard that keeps stray rows raw and unnormalized ([source filter](apple-health.md#source-filter), [J22.25](../plan/E22-ios-app/J22.25-apple-health-source-filter.md)). The relayed classification and the rules' relayed exclusion stay as the second line of defence.
- **One screen** joins local state (enabled groups, per-type anchors, last upload, queue) with the server's view of this device (`GET /devices`: last seen, possibly-denied types, resync requests).
- **Lifecycle (J22.14):** `AppDelegate` owns `AppState`, so `ThisDevice` (Features/AppleHealth) exists at launch; `didFinishLaunching` starts it (observer queries with background delivery for the enabled types, server-requested resets, a sync), the scene's `.backgroundTask(.appRefresh)` runs the refresh task, and becoming active syncs. One run at a time per phone; each run ends with the heartbeat checkpoint. A `401` to the device token stops sync and shows "Pair again". Sign-out leaves all of this running.
- **Upgrade in place:** the app keeps Bridge's bundle identifier (`org.vitamux.healthbridge`) and Keychain service, so an installed Bridge updates into the app with its device token, anchors and HealthKit authorizations intact. Owners who changed the bundle id keep theirs.

## Apple Watch

There is no watchOS app. Apple Watch writes into the iPhone's Health store, which the app already reads, so Watch data arrives the same way and keeps its origin: `device` (`HKDevice`, model `Watch`) and `source_revision.product_type` (`Watch7,1`, …). Rules can already prefer or exclude it (`provider: apple_health, device_type: watch`). The contract is [ADR-0024](../adr/0024-watch-data.md) (proposed until the owner accepts it; [J22.15](../plan/E22-ios-app/J22.15-watch-data-contract.md)).

- **Coverage:** type registry v2 = v1 plus the Watch types v1 lacks ([table](../adr/0024-watch-data.md#type-registry-v2)). v1 types keep their groups and defaults; six new groups (ECG, beats, routes, cycle, symptoms, mind) are off until turned on. Types the iOS version does not know are skipped, as today.
- **Payload:** still `healthkit.samples.v1`, extended only with optional fields ([fields](../adr/0024-watch-data.md#payload-fields), [apple-health › Payload](apple-health.md#payload)). Activity summaries have no UUID or anchor: the app re-reads the last 7 days on each sync, keyed by a UUIDv5 of the day, so a changed day replaces the old row.
- **Storage:** no new tables ([ADR-0024](../adr/0024-watch-data.md#storage-no-new-tables)). Beats become `rr_interval` samples (not resolved); ECG recordings and routes are `health_events` with their waveform or route in a blob (`health_events.file_blob_sha256`); workout events and activities go to `workout_segments`; activity summaries become daily values with Apple's goals in `context`.
- **Reading:** `GET /events/{id}/waveform`, `GET /workouts/{id}/route` and the existing measurement endpoints. Exports, deletion and purge cover the new blobs.
- **Views:** in the app and the panel, an ECG strip at the standard paper scale (shown, never interpreted), an RR plot, activity rings as plain value-against-goal bars, and a route map. The app draws routes on MapKit, which fetches Apple map tiles for the area; the panel draws them as a plain path without third-party tiles. An **Apple Watch** card on the Apple Health screen lists what the Watch contributed per type, with the last sample time.
- **Privacy:** the [opt-in rules](../adr/0024-watch-data.md#privacy-opt-in). Routes are the most sensitive data Vitamux holds; like other canonical data they are not app-encrypted, which [security.md](security.md#threat-model) states.

## Offline cache, widgets and notifications

- **Cache:** one middleware, `ResponseCache` in VitamuxKit's `Core/`, stores the GET responses for the dashboard, catalogue, inventory, metric ranges, specialised views, lab results and connections per server and session in the app group container (file protection `completeUntilFirstUserAuthentication`, no iCloud backup), with a fixed 100 MB cap; the least recently used go first. Network first: when the server cannot be reached the stored answer is served, and the shell shows "Offline, showing data from <time>"; a pull to refresh never falls back; mutations need the network. Sign-out, a `401`, a new sign-in or Settings › This app clears it. Screens have no cache code ([J22.19](../plan/E22-ios-app/J22.19-offline-cache.md)).
- **Widgets:** WidgetKit small, medium and lock-screen accessory widgets for one or several metric cards (value, neutral delta, sparkline, status). Configured with App Intents from the dashboard card list. Values are `privacySensitive` and redacted while locked by default. Timelines read the cache and refresh it within the widget budget. A tap deep-links to the metric.
- **Notifications:** local only, from `BGAppRefreshTask` and foreground refreshes: connection needs attention, permanently failed job, lab document ready for review, stale backup, Apple Health upload stalled. Sent on a state change only, never containing health values, each category toggled in the app.

## Security and privacy

- No values, tokens or URLs with secrets in logs (`os.Logger` with private interpolation); a test scans debug output with synthetic data.
- `PrivacyInfo.xcprivacy` declares the required-reason APIs; no tracking, no third-party SDKs.
- Secrets typed into the app (passwords, TOTP, AI keys, Withings secrets, provider MFA codes) are sent once and never cached, logged or kept in view state.
- App Transport Security on; certificate pinning stays deferred, as for Bridge.
- The threat model in [security.md](security.md#threat-model) gains a row for a lost phone: app lock, short Keychain accessibility, and revoking the app session and device token from the panel.

## Testing and release

- `swift test` for both packages on macOS in CI (the existing `swift` job), plus an `xcodebuild` build and UI tests on a simulator against the fake server.
- A stack smoke runs a simulator against a real server on a throwaway database with synthetic data ([J22.22](../plan/E22-ios-app/J22.22-quality-gates.md)).
- A physical-device campaign repeats the [Bridge checklist](../apple-health-device-checklist.md) and adds widgets, notifications, app lock and the upgrade from Bridge ([J22.23](../plan/E22-ios-app/J22.23-device-campaign-release.md)).
