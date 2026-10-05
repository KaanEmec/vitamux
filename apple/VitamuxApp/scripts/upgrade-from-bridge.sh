#!/usr/bin/env bash
# Installs the app over a Vitamux Bridge build on a simulator and checks that the paired device
# token and the sync anchors survive (J22.5). Synthetic values only: Bridge's -uitest stores.
#   apple/VitamuxApp/scripts/upgrade-from-bridge.sh ["iPhone 17 Pro"]
set -euo pipefail

device="${1:-iPhone 17 Pro}"
here="$(cd "$(dirname "$0")/.." && pwd)"
build="$(mktemp -d)"
bundle=org.vitamux.healthbridge
suite=org.vitamux.healthbridge.uitest
anchor=anchor.HKQuantityTypeIdentifierHeartRate
destination="platform=iOS Simulator,name=$device"

xcrun simctl boot "$device" 2>/dev/null || true
xcrun simctl uninstall "$device" "$bundle" || true

echo "== Bridge: pair (synthetic token) and keep an anchor"
(cd "$here/../HealthBridgeApp" && xcodegen generate --quiet)
xcodebuild -quiet -project "$here/../HealthBridgeApp/HealthBridgeApp.xcodeproj" -scheme HealthBridgeApp \
  -destination "$destination" -derivedDataPath "$build/bridge" build
xcrun simctl install "$device" "$build/bridge/Build/Products/Debug-iphonesimulator/HealthBridgeApp.app"
xcrun simctl launch "$device" "$bundle" -uitest -uitest-paired >/dev/null
sleep 5
xcrun simctl terminate "$device" "$bundle"
# The container moves on every install; its contents stay.
prefs() { echo "$(xcrun simctl get_app_container "$device" "$bundle" data)/Library/Preferences/$suite"; }
xcrun simctl spawn "$device" defaults write "$(prefs)" "$anchor" -data 0102030405

# Runs the upgrade UI test; fails if it was skipped instead of run.
check() {
  env TEST_RUNNER_VITAMUX_UPGRADE_CHECK=1 "$@" xcodebuild -project "$here/Vitamux.xcodeproj" -scheme Vitamux \
    -skipPackagePluginValidation -destination "$destination" -derivedDataPath "$build/app" \
    -only-testing:VitamuxUITests/UpgradeFromBridgeUITests test >"$build/test.log" 2>&1 || { tail -40 "$build/test.log"; exit 1; }
  grep -q "testBridgePairingSurvivesTheUpgrade\]' passed" "$build/test.log" || { echo "upgrade test did not run"; exit 1; }
}

echo "== Vitamux: install over Bridge and read its pairing"
(cd "$here" && xcodegen generate --quiet)
check
xcrun simctl spawn "$device" defaults read "$(prefs)" "$anchor" >/dev/null || { echo "anchor lost in the upgrade"; exit 1; }

echo "== Vitamux: unpair clears the token and the anchors"
check TEST_RUNNER_VITAMUX_UPGRADE_UNPAIR=1
if xcrun simctl spawn "$device" defaults read "$(prefs)" "$anchor" >/dev/null 2>&1; then
  echo "anchor still there after unpairing"; exit 1
fi
echo "upgrade from Bridge: ok"
