#!/usr/bin/env bash
# Refreshes the README's screenshots (docs/images/ios) from the fake server's synthetic data
# (J22.24): runs ScreenshotUITests on a simulator in dark mode with a clean status bar, then
# shrinks each PNG to 390 points wide.
#   apple/VitamuxApp/scripts/screenshots.sh ["iPhone 17 Pro"]
set -euo pipefail

device="${1:-iPhone 17 Pro}"
here="$(cd "$(dirname "$0")/.." && pwd)"
root="$(git -C "$here" rev-parse --show-toplevel)"
out="$root/docs/images/ios"
shots="$(mktemp -d)"

xcrun simctl boot "$device" 2>/dev/null || true
xcrun simctl ui "$device" appearance dark
xcrun simctl status_bar "$device" override --time 9:41 --batteryState charged --batteryLevel 100 \
  --cellularBars 4 --wifiBars 3 --dataNetwork wifi

(cd "$here" && xcodegen generate --quiet)
TEST_RUNNER_SCREENSHOT_DIR="$shots" xcodebuild -quiet -project "$here/Vitamux.xcodeproj" -scheme Vitamux \
  -skipPackagePluginValidation -destination "platform=iOS Simulator,name=$device" \
  -only-testing:VitamuxUITests/ScreenshotUITests test

xcrun simctl status_bar "$device" clear
mkdir -p "$out"
for png in "$shots"/*.png; do
  sips --resampleWidth 390 "$png" --out "$out/$(basename "$png")" >/dev/null
done
ls -l "$out"
