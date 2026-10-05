# Vitamux (iOS app)

The native iPhone client of a self-hosted Vitamux server ([E22](../../docs/plan/E22-ios-app/README.md), [design](../../docs/architecture/ios-app.md), [ADR-0023](../../docs/adr/0023-ios-app.md)). It replaces [Vitamux Bridge](../HealthBridgeApp) once J22.14 has moved Bridge's screens; until then this is a skeleton.

## Build from source

```sh
brew install xcodegen
cd apple/VitamuxApp
xcodegen generate
xcodebuild -project Vitamux.xcodeproj -scheme Vitamux -skipPackagePluginValidation \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' build
```

`project.yml` is the source of truth; `Vitamux.xcodeproj` and `Sources/Info.plist` are generated and not committed. Set `DEVELOPMENT_TEAM` for a device build. The bundle id stays Bridge's (`org.vitamux.healthbridge`) so an installed Bridge upgrades in place.

## Layout

| Path | Contents |
| --- | --- |
| `Sources/App/` | `VitamuxApp` (entry), `AppState` (the one shared object), `Route` (every linkable screen, `vitamux://` parsing), `RootView` (tabs and stacks) |
| `Sources/Features/<Feature>/` | A feature's views and, where a screen loads or changes data, its `@Observable` model |
| `Sources/Shared/` | Components used by more than one feature (`ProblemView`) |
| `UITests/` | UI tests, one file per screen |
| [`../VitamuxKit`](../VitamuxKit) | API client (J22.4), `Core` (`Loadable`, `Problem`), charts (J22.6) |

The app target is main-actor by default (`SWIFT_DEFAULT_ACTOR_ISOLATION`), so views and models need no `@MainActor`. No repositories, coordinators, view-model protocols or DI containers ([lean architecture](../../docs/architecture/ios-app.md#lean-architecture)).

## Worked example

System status (`vitamux://settings/system`) is the pattern every screen copies:

1. **Model** `Features/Settings/SystemStatusModel.swift`: an `@Observable` class holding one `Loadable<Value>` with `private(set)`, and an `async` `load()` that assigns `await Loadable { … }`. Later models call the generated client inside that closure.
2. **Screen** `Features/Settings/SystemStatusView.swift`: owns the model as `@State`, loads in `.task`, switches on the `Loadable`, shows failures with `ProblemView`, and splits sections into small private views.
3. **Route**: a case in `Route`, its path in `Route.init?(url:)` (the panel path, listed in [ios-app-screens](../../docs/architecture/ios-app-screens.md)), its tab in `Route.tab`, and its screen in `RouteView`.
4. **UI test** `UITests/SystemStatusUITests.swift`: open the deep link and check the screen by accessibility identifier.
