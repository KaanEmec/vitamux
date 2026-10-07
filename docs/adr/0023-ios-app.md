# ADR-0023 Vitamux iOS app

Status: Accepted (2026-10-07) · Date: 2026-10-05 · Deciders: owner

## Context
Epic [E22](../plan/E22-ios-app/README.md) adds a native iPhone client with full panel parity that also replaces the Vitamux Bridge app ([ADR-0014](0014-healthkit-contract.md), [apple-health](../architecture/apple-health.md)). It must stay small enough for one contributor. Details live in [ios-app](../architecture/ios-app.md); the screens in [ios-app-screens](../architecture/ios-app-screens.md).

## Decision
- **Platform.** Native SwiftUI, Swift 6 language mode with strict concurrency, iOS 18 minimum, iPhone only, portrait-first. No third-party SDKs.
- **Lean architecture** ([rules](../architecture/ios-app.md#lean-architecture)):
  - Two packages: `HealthBridgeKit` (unchanged role: HealthKit sync, reusable by others) and `VitamuxKit` with **one** library target (folders `API/`, `Core/`, `Charts/`) and one test target.
  - One app target, `apple/VitamuxApp`, with a folder per feature under `Sources/Features/`; the app target is main-actor by default.
  - A screen that loads or changes data has one `@Observable` model that calls the generated client directly and keeps its state in one `Loadable<Value>`; failures show through one `ProblemView`.
  - One `AppState` in the environment (server profile, session, client, router); one `Route` enum that every deep link, widget, notification and the OAuth return parse into; a `NavigationStack` per tab.
  - Allowed packages: Apple's swift-openapi generator, runtime and URLSession transport, plus the dependencies they resolve (J22.4 pins the list; CI rejects anything else in `Package.resolved`).
  - Ruled out: repositories, use-case or service layers, coordinators, view-model protocols, protocols added only for testing, DI containers, Combine pipelines, Core Data, SwiftData, UI kits, analytics, feature flags and plugin points. A shared component appears on its second use.
  - The [worked example](../../apple/VitamuxApp/README.md#worked-example) (screen, model, route, UI test) is the pattern later jobs copy.
  - Kept after the lean review ([J22.22](../plan/E22-ios-app/J22.22-quality-gates.md)): `Notifier` as a second environment object (the notification centre's delegate, created at launch with `AppState`); `ConnectionActions` and `Pins`, the models of a control two screens share (sync and reauthorize; pin), each owned by the screen showing it; `GatedStore` and the UI tests' `FakeHealthStore`, conformances to HealthBridgeKit's `HealthStore` (the package's HealthKit seam, not an app protocol); the `Coordinator`s of the PDFKit and VisionKit bridges, which are `UIViewRepresentable` plumbing, not navigation. App-wide formats and labels live once in `Shared/Format.swift`; feature copy stays in its folder.
- **Replaces Bridge.** `apple/VitamuxApp` replaces `apple/HealthBridgeApp`, which stays until J22.14 has moved its screens.
- **Upgrade in place.** The app keeps Bridge's bundle id (`org.vitamux.healthbridge`) and Keychain service, so an installed Bridge updates into the app with its device token, anchors and HealthKit authorizations.
- **Bearer app sessions** ([J22.2](../plan/E22-ios-app/J22.2-app-sessions.md)):
  - `POST /auth/login` with `{"client": "app", "device_name"}` answers a token `vmx_ses_<id>_<secret>` in the body, no cookie. It is a row in the sessions table (`kind = app`, a name), stored as SHA-256, listed and ended in Settings › Security like a browser session; password change ends it too.
  - Lifetimes: 30 days idle, 90 days absolute (`VITAMUX_APP_SESSION_IDLE`, `VITAMUX_APP_SESSION_MAX`). Password, TOTP, recovery codes and throttling are the panel's.
  - It is a `session` principal on session-only routes; bearer requests skip CSRF.
  - Storage: a Keychain generic-password item, `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, in an access group shared with the widget, separate from the device token. Any `401` outside login clears it.
  - Not cookies: `SameSite=Strict` cookies, CSRF headers and a 7-day absolute lifetime fit a browser, not a phone that refreshes in the background and from a widget. Not API keys: they are scope-based, long-lived, issued without TOTP, and by design never reach session-only routes (password, TOTP, sessions, connection auth).
- **Native OAuth return** ([J22.3](../plan/E22-ios-app/J22.3-native-auth-return.md)): `auth/begin` with `{"return": "app"}` answers a `redirect_url` to `GET /oauth/{provider}/start?ticket=…` (public, single use, short TTL), which sets the binding cookie inside `ASWebAuthenticationSession` and redirects to the provider. For an app-originated state the callback redirects to `vitamux://connections?connected=<provider>` or `?auth_error=<code>`. The scheme is a server constant, never taken from the request, so the callback cannot become an open redirect. Prompt steps stay plain JSON over the bearer session.
- **Generated client.** swift-openapi-generator runs as a build plugin over a copy of `api/openapi.yaml` made by `make openapi` with a CI drift check (the symlink was tried: the generator drops nullable `$ref` fields, so the copy step rewrites them). Nothing generated is committed.
- **Charts.** Swift Charts behind `VitamuxKit/Charts`, with `chartFor(metric)` mirroring the panel's grammar; one shared fixture is tested by `web` and VitamuxKit.
- **Boundaries.** The offline cache is files in the app group (per server and user, 100 MB, cleared on sign-out); widgets read that cache and redact while locked; notifications are local only, on state changes, never with health values. No APNs or other server push.
- **Design after parity.** Screens use stock SwiftUI with the panel's status and source cues; the visual pass is [J22.24](../plan/E22-ios-app/J22.24-design-pass.md).

## Alternatives considered
- **Cross-platform UI (React Native, Flutter) or a web view**: no HealthKit background delivery without native code anyway, and a second toolchain for a small community.
- **MVVM with protocols, coordinators and a DI container**: more files per screen and nothing gained at this size; SwiftUI's environment and `@Observable` cover it.
- **Hand-written API client**: drifts from the spec; the generated one cannot.
- **A new bundle id**: forces Bridge users to re-pair, re-authorize HealthKit and re-pull history.
- **A free custom scheme per install or a universal link**: a configurable return target is an open-redirect risk, and universal links need an `apple-app-site-association` file on every self-hosted server.
- **iPad and watchOS apps**: out of scope; Watch data comes through the iPhone ([J22.15](../plan/E22-ios-app/J22.15-watch-data-contract.md), ADR-0024).

## Consequences
- The server gains app sessions and the OAuth start route (J22.2, J22.3); `api/authz.yaml` and `TestAuthzMatrixEnforced` gain the app-session caller.
- Every new panel screen needs a parity row and a phone screen ([parity matrix](../architecture/ios-app.md#parity-matrix)).
- A layer, package or abstraction beyond these rules needs an amendment to this ADR (checked in [J22.22](../plan/E22-ios-app/J22.22-quality-gates.md)).
