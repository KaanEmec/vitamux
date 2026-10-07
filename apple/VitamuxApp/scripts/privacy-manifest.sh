#!/usr/bin/env bash
# Privacy-manifest validation (J22.22) for the app and the widget extension: each
# PrivacyInfo.xcprivacy is a valid plist that declares no tracking, no tracking domains and no
# collected data (data goes only to the owner's server); every accessed-API category it lists
# uses Apple's approved reason codes; every required-reason API the target's sources call is
# declared; and, given a build, both bundles ship their manifest.
#   apple/VitamuxApp/scripts/privacy-manifest.sh [path/to/Vitamux.app]
set -euo pipefail
cd "$(dirname "$0")/.."
apple=..
status=0
fail() { echo "privacy manifest: $*"; status=1; }

# Category: approved reasons (developer.apple.com, "Describing use of required reason API").
reasons() {
	case $1 in
	UserDefaults) echo "CA92.1 1C8F.1 C56D.1 AC6B.1" ;;
	FileTimestamp) echo "DDA9.1 C617.1 3B52.1 0A2A.1" ;;
	SystemBootTime) echo "35F9.1 8FFB.1 3D61.1" ;;
	DiskSpace) echo "85F4.1 E174.1 7D9E.1 B728.1" ;;
	ActiveKeyboards) echo "3EC4.1 54BD.1" ;;
	esac
}
# Category: what calls it in Swift.
pattern() {
	case $1 in
	UserDefaults) echo 'UserDefaults' ;;
	FileTimestamp) echo 'creationDate|modificationDate|ModificationDate|CreationDate|attributesOfItem|fileModificationDate' ;;
	SystemBootTime) echo 'systemUptime|mach_absolute_time' ;;
	DiskSpace) echo 'volumeAvailableCapacity|volumeTotalCapacity|systemFreeSize|systemSize' ;;
	ActiveKeyboards) echo 'activeInputModes' ;;
	esac
}

# check MANIFEST SOURCE...: the manifest against the Swift files under SOURCE.
check() {
	local manifest=$1 json category declared
	shift
	plutil -lint -s "$manifest" || { fail "$manifest is not a valid plist"; return; }
	json=$(plutil -convert json -o - "$manifest")
	[ "$(jq '.NSPrivacyTracking' <<<"$json")" = false ] || fail "$manifest: NSPrivacyTracking must be false"
	[ "$(jq -c '.NSPrivacyTrackingDomains' <<<"$json")" = '[]' ] || fail "$manifest: tracking domains listed"
	[ "$(jq -c '.NSPrivacyCollectedDataTypes' <<<"$json")" = '[]' ] || fail "$manifest: collected data types listed"
	declared=$(jq -r '.NSPrivacyAccessedAPITypes[]? | .NSPrivacyAccessedAPIType | sub("^NSPrivacyAccessedAPICategory"; "")' <<<"$json")
	for category in $declared; do
		[ -n "$(reasons "$category")" ] || { fail "$manifest: unknown category $category"; continue; }
		for reason in $(jq -r --arg c "NSPrivacyAccessedAPICategory$category" '.NSPrivacyAccessedAPITypes[] | select(.NSPrivacyAccessedAPIType == $c) | .NSPrivacyAccessedAPITypeReasons[]' <<<"$json"); do
			grep -qw "$reason" <<<"$(reasons "$category")" || fail "$manifest: $reason is not a reason for $category"
		done
	done
	for category in UserDefaults FileTimestamp SystemBootTime DiskSpace ActiveKeyboards; do
		local used
		used=$(grep -rlE --include='*.swift' "$(pattern "$category")" "$@" | grep -v '/Tests/' | head -3 || true)
		if [ -n "$used" ] && ! grep -qx "$category" <<<"$declared"; then
			fail "$manifest: $category is used ($(echo "$used" | tr '\n' ' ')) but not declared"
		fi
	done
}

# The app compiles its sources, the widgets' shared files and both kits in.
check Sources/PrivacyInfo.xcprivacy Sources Widgets/Shared "$apple/VitamuxKit/Sources" "$apple/HealthBridgeKit/Sources"
check Widgets/PrivacyInfo.xcprivacy Widgets Sources/Shared/MetricTile.swift

if [ $# -gt 0 ]; then
	app=$1
	[ -f "$app/PrivacyInfo.xcprivacy" ] || fail "$app has no PrivacyInfo.xcprivacy"
	[ -f "$app/PlugIns/VitamuxWidgets.appex/PrivacyInfo.xcprivacy" ] || fail "the widget extension in $app has no PrivacyInfo.xcprivacy"
fi
[ "$status" -eq 0 ] && echo "privacy manifests: ok (app and widget${1:+, both bundles ship theirs})"
exit "$status"
