# Vitamux Bridge (iOS app)

Minimal SwiftUI app that reads Apple Health on your iPhone and uploads it to your own Vitamux server ([J15.5](../../docs/plan/E15-apple-health/J15.5-app.md), [design](../../docs/architecture/apple-health.md)). All sync logic lives in [`../HealthBridgeKit`](../HealthBridgeKit). There is no App Store or TestFlight build: you build it yourself.

## Build from source

You need a Mac with Xcode (iOS 17 SDK or newer), a physical iPhone on iOS 17 or newer, and an Apple Developer Program team. A paid membership is the supported path ([why](../../docs/architecture/apple-health.md#distribution)); a free account works for a development build, but its profile expires after 7 days and background delivery on it is unconfirmed.

```sh
brew install xcodegen
cd apple/HealthBridgeApp
```

1. In `project.yml` set `DEVELOPMENT_TEAM` to your team id, and change `PRODUCT_BUNDLE_IDENTIFIER` (`org.vitamux.healthbridge`) to one you own if your team cannot register it.
2. `xcodegen` generates `HealthBridgeApp.xcodeproj`, `Sources/Info.plist` and the entitlements file. None are committed; `project.yml` is the source of truth.
3. `open HealthBridgeApp.xcodeproj`, pick your iPhone, and run. With automatic signing Xcode registers the App ID and enables the capabilities `project.yml` declares:
   - HealthKit, and HealthKit background delivery;
   - Background Modes: Background fetch (for the refresh task);
   - Info.plist strings: `NSHealthShareUsageDescription` (read only, there is no write scope) and `NSCameraUsageDescription` (QR scan only).
4. On first launch, trust the developer certificate if iOS asks (Settings > General > VPN & Device Management).

## Pair with your server

1. In the Vitamux web panel open Settings > Devices and create a pairing code (single use, 10 minutes). It shows a QR code and the code.
2. In the app scan the QR code, or type the server address (https only) and the code.
3. Turn on the metric groups you want. iOS shows its permission sheet for that group's types, and the app pulls their history. The app shows "Requested", never "Granted", because iOS does not reveal whether a read was allowed. A type that stays silent for 7 days is flagged "possibly denied" on the server.

The QR payload is the JSON `{"url":"<public base URL>","code":"XXXX-XXXX"}`. On launch and after each sync the app reads `GET /api/ingest/v1/devices/self` to apply anchor resets requested on the server, and posts a heartbeat checkpoint (anchor hashes only).

## Privacy

- **Read only.** The app never writes to Apple Health. It reads only the types in the metric groups you turn on, and you can withdraw access any time in Settings > Health > Data Access & Devices.
- **Sent where.** Samples, with their source app, device and timestamps, go over HTTPS to the server address you paired with, which you run yourself. The device token lives in this phone's Keychain.
- **Nothing else.** No analytics, ads, third-party services or other servers. The camera is used only to scan the pairing QR code.

The same text is on the app's Privacy screen.

## Background timing

iOS decides when background delivery and the refresh task run. Some types update at most hourly, delivery can pause in Low Power Mode or after a force-quit, and the Simulator never delivers in the background. Promise eventual sync only. Measured behaviour is recorded in the [device checklist](../../docs/apple-health-device-checklist.md); the mechanism is in [apple-health.md](../../docs/architecture/apple-health.md#sync-algorithm).

## UI tests

Launch argument `-uitest` swaps in a fake HealthStore and fake transport (no HealthKit prompts, no server); `-uitest-paired` also starts already paired.

```sh
xcodebuild test -project HealthBridgeApp.xcodeproj -scheme HealthBridgeApp \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro'
```
