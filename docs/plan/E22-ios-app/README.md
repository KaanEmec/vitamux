# E22 Vitamux iOS app (v0.4.0)

Release: v0.4.0 · Status: shipped (2026-10-07; the device campaign [J22.23](J22.23-device-campaign-release.md) and gate G8 stay open) · Depends on: E15, E20, E21 (read-only screens can start now) · [Plan index](../README.md)
Read first: [ios-app](../../architecture/ios-app.md), [apple-health](../../architecture/apple-health.md), [frontend](../../architecture/frontend.md)

**Objective:** A native iPhone app that signs in to any Vitamux server like the panel, offers every panel feature with metric visualisation first, and is the device that syncs Apple Health, including everything Apple Watch records. It replaces the Vitamux Bridge app, reuses HealthBridgeKit, and stays small enough for one contributor to understand.

Owner decisions (2026-10-04):
- The app replaces Vitamux Bridge; HealthBridgeKit stays a reusable package.
- Sign-in with username, password and TOTP gives a bearer app session, not a cookie.
- Build from source with the owner's paid Apple team. App Store and TestFlight stay deferred.
- Full panel parity, including admin and ops settings.
- Extras: home-screen widgets, offline read cache, local notifications, lab PDFs from the camera or Files.
- Apple Watch: every type the Watch records, read on the iPhone (no watchOS app), including ECG, beat-to-beat series, workout routes, cycle tracking and State of Mind as opt-in groups.
- Apple Health has a visible take/ignore setting per app and type, so a provider connected directly is never collected twice through Apple Health ([J22.25](J22.25-apple-health-source-filter.md)).
- Metrics can be viewed inside a day at their own resolution: heart rate down to 30-second buckets and raw readings, steps as 30-minute bars, once-a-day metrics not at all ([J22.26](J22.26-intraday-views.md)).
- A lean, clean architecture: plain SwiftUI, two packages, one app target, a folder per feature, no extra layers or third-party packages ([lean architecture](../../architecture/ios-app.md#lean-architecture)).
- iOS 18 minimum, iPhone only.
- v0.4.0, after E20 (v0.3.0).
- Design comes after parity: stock SwiftUI first, then a design pass ([J22.24](J22.24-design-pass.md)).

**Outputs:**
- ADR-0023 (app) and ADR-0024 (Watch data), and the parity matrix in [ios-app](../../architecture/ios-app.md#parity-matrix);
- app sessions and the native connection-auth return on the server;
- `apple/VitamuxKit` (generated API client, core, chart kit);
- `apple/VitamuxApp` (app, widget extension, UI tests), replacing `apple/HealthBridgeApp`;
- every panel screen on the phone, plus Apple Health, widgets, cache and notifications;
- type registry v2, its normalizer and the Watch views in the app and the panel;
- intraday resolution in the catalogue, the 30-second bucket, and the Day view in both clients;
- a device test report and the v0.4.0 release.

## Acceptance
- **Sign-in:** on a fresh install the owner types a server URL, signs in with password and TOTP (or a recovery code), and lands on the dashboard. The session shows in the panel's Settings › Security and ending it there signs the app out at its next request. A server below the minimum API version gets a clear message.
- **Parity:** every row of the [parity matrix](../../architecture/ios-app.md#parity-matrix) works on the phone against the same endpoints, with the same confirmations, field errors and audit records as the panel.
- **Visualisation:** every metric code, group, event type and lab analyte in the synthetic dataset opens a chart picked by the grammar, with selection, status markers, source series, baseline, coverage and a table fallback. The grammar fixture passes in both `web` and the app's chart kit.
- **Intraday:** every metric with an intraday resolution opens a Day view in both clients; heart rate zooms from 1-minute buckets to 30 seconds and raw samples, steps show 30-minute bars, and a bucket's explanation matches the all-sources view.
- **Rules:** from a metric chart the owner reorders sources, changes strategy, window or coverage, sees the draft overlaid, then saves, activates or reverts. The full builder works with live preview.
- **Apple Health:** a signed-in owner pairs this iPhone in one tap. Selected types sync incrementally with deletions, in the background on a physical device. An installed Bridge upgrades in place without re-pairing, re-authorizing or a full re-pull.
- **No double counting:** with WHOOP or Garmin connected directly, their copies in Apple Health are ignored by default, visibly, and the owner can take or ignore any Health app per type from the phone or the panel.
- **Apple Watch:** on a physical iPhone with a paired Watch, every registry v2 group the owner turns on arrives with the Watch as its device. ECG strips, RR series, activity rings and routes show in both clients. A rule can prefer or exclude the Watch. Sensitive groups stay off until turned on.
- **Extras:** widgets show cached dashboard values, redacted while locked; screens read from the cache offline, marked stale; notifications fire on state changes and carry no health values; a scanned or shared PDF reaches lab review.
- **Lean:** the code follows the [lean architecture](../../architecture/ios-app.md#lean-architecture) rules, and CI rejects any package outside the allowed list.
- **Quality:** UI tests cover every flow on the fake server; the stack smoke passes; VoiceOver and the largest Dynamic Type size work on every screen; the dashboard opens in under 1 s on the synthetic 3-year dataset; no health values or secrets in logs.
- **Neutral copy:** no screen judges a value, an ECG or a rhythm result.
- Gate G8 passed.

## Parallelism
- J22.1 and J22.15 can start now. J22.2, J22.3 and J22.4 follow J22.1, and J22.6 can start once J22.4's client builds.
- After J22.5: J22.7–J22.13 run in parallel; J22.11's setup wizards follow E20's API.
- J22.14 can start right after J22.5, since it only needs sign-in and HealthBridgeKit.
- After J22.15: J22.16 (kit) and J22.17 (server) run in parallel with everything else; J22.18 follows both.
- J22.19 lands before J22.20 and J22.21.
- J22.25 follows J22.14 and J22.16, and J22.26 follows J22.8; both land before J22.22. J22.26's catalogue and API parts (T22.26.1, T22.26.2) can start now.

## Jobs
| Job | Title | Depends on | Gate | Status |
| --- | --- | --- | --- | --- |
| [J22.1](J22.1-app-adr-parity.md) | App ADR, lean rules, parity matrix and screen map | None | None | done |
| [J22.2](J22.2-app-sessions.md) | Backend: bearer app sessions | J22.1 | None | done |
| [J22.3](J22.3-native-auth-return.md) | Backend: native connection auth, pairing and version checks | J22.1, J22.2 | None | done |
| [J22.4](J22.4-vitamuxkit-client.md) | VitamuxKit: generated client, core and fake server | J22.1 | None | done |
| [J22.5](J22.5-app-shell-signin.md) | App project, shell, sign-in and Bridge migration | J22.2, J22.4 | None | done |
| [J22.6](J22.6-chart-kit.md) | Native chart kit and chart grammar | J22.4 | None | done |
| [J22.7](J22.7-dashboard.md) | Dashboard | J22.5, J22.6 | None | done |
| [J22.8](J22.8-explore-metric-detail.md) | Explore, metric detail, overrides and provenance | J22.5, J22.6 | None | done |
| [J22.9](J22.9-specialised-views.md) | Specialised views | J22.8 | None | done |
| [J22.10](J22.10-rules.md) | Rules, rule lens and rule builder | J22.8 | None | done |
| [J22.11](J22.11-sources.md) | Sources: connections, connect and guided setup | J22.3, J22.5; E20 for the wizards | None | done |
| [J22.12](J22.12-lab.md) | Lab documents, review and results | J22.5, J22.6 | None | done |
| [J22.13](J22.13-settings.md) | Settings parity | J22.5 | None | done |
| [J22.14](J22.14-apple-health.md) | Apple Health in the app | J22.3, J22.5 | None | done |
| [J22.15](J22.15-watch-data-contract.md) | Apple Watch data contract (ADR-0024) | None | None | done |
| [J22.16](J22.16-watch-kit.md) | HealthBridgeKit: type registry v2 and Watch readers | J22.15 | None | done |
| [J22.17](J22.17-watch-normalizer.md) | Server: Watch data normalizer, storage and endpoints | J22.15 | None | done |
| [J22.18](J22.18-watch-views.md) | Apple Watch views in the app and the panel | J22.6, J22.14, J22.17 | None | done |
| [J22.19](J22.19-offline-cache.md) | Offline read cache | J22.4 | None | done |
| [J22.20](J22.20-widgets.md) | Home-screen and lock-screen widgets | J22.7, J22.19 | None | done |
| [J22.21](J22.21-notifications.md) | Local notifications | J22.14, J22.19 | None | done |
| [J22.22](J22.22-quality-gates.md) | Quality gates and CI | J22.7–J22.21, J22.25, J22.26 | None | done |
| [J22.23](J22.23-device-campaign-release.md) | Device campaign, docs and v0.4.0 | J22.22, G6 | G8 | todo |
| [J22.24](J22.24-design-pass.md) | Visual design pass (after parity) | J22.22 | None | done |
| [J22.25](J22.25-apple-health-source-filter.md) | Apple Health source filter: take or ignore per app and type | J22.3, J22.14, J22.16 | None | done |
| [J22.26](J22.26-intraday-views.md) | Intraday views: one day at the metric's own resolution | J22.6, J22.8 | None | done |

## Out of scope
- A watchOS app, complications, or uploads from the Watch itself: Watch data comes through the iPhone.
- iPad layouts, Mac Catalyst.
- HealthKit write-back, clinical records, types that need another entitlement.
- APNs or any server push; several servers or users signed in at once.
- App Store or TestFlight distribution; certificate pinning.
- Features the panel lacks: scores, goals set by Vitamux, insights, advice, ECG or rhythm interpretation.
- The Apple Health export importer stays a CLI command; uploading an export from the phone is a later job if the panel gets one.
