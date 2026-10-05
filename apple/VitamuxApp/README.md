# Vitamux (iOS app)

The native iPhone client of a self-hosted Vitamux server ([E22](../../docs/plan/E22-ios-app/README.md), [design](../../docs/architecture/ios-app.md), [ADR-0023](../../docs/adr/0023-ios-app.md)). It replaces [Vitamux Bridge](../HealthBridgeApp) once J22.14 has moved Bridge's screens. J22.5 built the project, the server step and sign-in, the shell (tabs, search, sync status, theme, sign-out), the `vitamux://` router and app lock; screens later jobs build show a placeholder.

## Build from source

```sh
brew install xcodegen
cd apple/VitamuxApp
xcodegen generate
xcodebuild -project Vitamux.xcodeproj -scheme Vitamux -skipPackagePluginValidation \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' build test
```

`project.yml` is the source of truth; `Vitamux.xcodeproj`, the `Info.plist`s and the entitlements are generated and not committed. Targets: the app `Vitamux`, the widget extension stub `VitamuxWidgets` and `VitamuxUITests`. Set `DEVELOPMENT_TEAM` for a device build. The bundle id and Keychain service stay Bridge's (`org.vitamux.healthbridge`), and the app's Keychain access group is its own identifier (shared with the widget), so an installed Bridge upgrades in place with its device token and anchors: `scripts/upgrade-from-bridge.sh` checks that on a simulator.

## UI tests

UI tests launch the app with `-uitest`: the kit's fake server (`FakeServer.uiTestServers`: `fake.vitamux.test`, plus `old.vitamux.test` from before the version handshake, `other.vitamux.test` that is not Vitamux, and any other `*.vitamux.test` unreachable), an in-memory session and cleared preferences; app lock opens on the button. More arguments: `-uitest-totp` (two-factor on), `-uitest-paired` (a paired iPhone), `-uitest-keep-device` (keep what a Bridge build left). The link `vitamux://uitest/expire-sessions` ends the fake's sessions, for expiry mid-use. Open links in the running app with `openLink` (`UITestSupport.swift`); `XCUIApplication.open` relaunches it.

## Layout

| Path | Contents |
| --- | --- |
| `Sources/App/` | `VitamuxApp` (entry), `AppState` (the one shared object: server, session, client, router, theme, app lock), `Route` (every linkable screen, `vitamux://` parsing), `RootView` (sign-in, lock or shell; app-switcher blur), `ShellView` (tabs and stacks), `RouteView` (the screen per route) |
| `Sources/Features/<Feature>/` | A feature's views and, where a screen loads or changes data, its `@Observable` model (`SignIn`, `Shell` for search and sync status, `Lock`, `More`, `Settings`, `AppleHealth`, …) |
| `Sources/Shared/` | Components used by more than one feature (`ProblemView`, `PlaceholderView`, `AppMark`) |
| `Widgets/` | The widget extension (a stub until the widget job) |
| `UITests/` | UI tests, one file per screen |
| [`../VitamuxKit`](../VitamuxKit) | API client (J22.4), `Core` (`Loadable`, `Problem`), charts (J22.6) |

The app target is main-actor by default (`SWIFT_DEFAULT_ACTOR_ISOLATION`), so views and models need no `@MainActor`. No repositories, coordinators, view-model protocols or DI containers ([lean architecture](../../docs/architecture/ios-app.md#lean-architecture)).

## Worked example

System status (`vitamux://settings/system`) is the pattern every screen copies:

1. **Model** `Features/Settings/SystemStatusModel.swift`: an `@Observable` class holding one `Loadable<Value>` with `private(set)`, and an `async` `load()` that assigns `await Loadable { … }`. Later models call the generated client inside that closure.
2. **Screen** `Features/Settings/SystemStatusView.swift`: owns the model as `@State`, loads in `.task`, switches on the `Loadable`, shows failures with `ProblemView`, and splits sections into small private views.
3. **Route**: `Route.settings(.system)`: a case in `Route`, its path in `Route.init?(url:)` (the panel path, listed in [ios-app-screens](../../docs/architecture/ios-app-screens.md)), its tab in `Route.tab`, and its screen in `RouteView` (replacing the route's `PlaceholderView`).
4. **UI test** `UITests/SystemStatusUITests.swift`: sign in to the fake server, open the deep link, and check the screen by accessibility identifier.
