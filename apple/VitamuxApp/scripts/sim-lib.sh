# Shared by the iOS gate scripts (source it; J22.22): a throwaway simulator, the project generated
# and built for testing once, UI-test runs with a result bundle, and the simulator's log captured
# for the privacy scan. Set `work` (a temporary directory) first, then: sim_setup; app_build;
# app_test BUNDLE [xcodebuild args]; log_start FILE; log_stop. Call sim_cleanup from the EXIT trap.
#   SIM_UDID     use this simulator as is (never deleted); default: create one and delete it after
#   SIM_DEVICE   device type of the created simulator (default "iPhone 17 Pro")
#   SIM_RUNTIME  its runtime identifier (default: the newest installed iOS runtime)

app_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
udid=
created=
log_pid=

sim_setup() {
	if [ -n "${SIM_UDID:-}" ]; then
		udid=$SIM_UDID
	else
		udid=$(xcrun simctl create "Vitamux gates $$" "${SIM_DEVICE:-iPhone 17 Pro}" ${SIM_RUNTIME:+"$SIM_RUNTIME"})
		created=1
	fi
	xcrun simctl boot "$udid" 2>/dev/null || true
	xcrun simctl bootstatus "$udid" -b >/dev/null
	echo "simulator $udid ($(xcrun simctl list devices | grep -F "$udid" | sed -E 's/ \(.*//; s/^ *//'))"
}

sim_cleanup() {
	log_stop
	[ -z "$created" ] || { xcrun simctl shutdown "$udid" 2>/dev/null || true; xcrun simctl delete "$udid" 2>/dev/null || true; }
	created=
}

# xcb runs xcodebuild on the project for the simulator, appending to $work/xcodebuild.log.
xcb() {
	xcodebuild -project "$app_dir/Vitamux.xcodeproj" -scheme Vitamux -skipPackagePluginValidation \
		-destination "platform=iOS Simulator,id=$udid" -derivedDataPath "$work/derived" "$@" >>"$work/xcodebuild.log" 2>&1
}

app_build() {
	(cd "$app_dir" && xcodegen generate --quiet)
	echo "== build for testing (app, widget, tests)"
	xcb build-for-testing || { tail -40 "$work/xcodebuild.log"; return 1; }
}

# app_test BUNDLE [args]: runs tests one at a time (signposts and the log stay in order) into
# the result bundle; prints the test summary, and the log's tail on failure.
app_test() {
	local bundle=$1 start rc=0
	shift
	rm -rf "$bundle"
	start=$(wc -l <"$work/xcodebuild.log" 2>/dev/null || echo 0)
	xcb test-without-building -parallel-testing-enabled NO -test-timeouts-enabled YES -default-test-execution-time-allowance 600 -resultBundlePath "$bundle" "$@" || rc=$?
	tail -n +"$((start + 1))" "$work/xcodebuild.log" | grep -E "^Test Case .*(passed|failed|skipped)|^Executed|error:" | sed -E "s/^Test Case '-\[//; s/\]'//" | tail -200
	[ "$rc" -eq 0 ] || { echo "tests failed (result bundle: $bundle)"; tail -n +"$((start + 1))" "$work/xcodebuild.log" | tail -40; }
	return "$rc"
}

# log_start FILE: streams every line the app and its widget log, at every level, into FILE.
# Left out: XCTest's agent inside the app (com.apple.dt.xctest), which logs the values of the
# elements a test reads, and the accessibility runtime (com.apple.Accessibility), which logs an
# element's label at info level when a notification post fails while an assistive client (here
# XCUITest) is attached. Both are the system's, outside the app's control; info lines stay in
# memory on a device (J22.22 notes).
log_start() {
	xcrun simctl spawn "$udid" log stream --level debug --style compact \
		--predicate '(process == "Vitamux" OR process == "VitamuxWidgets") AND NOT (subsystem BEGINSWITH "com.apple.dt.xctest") AND NOT (subsystem BEGINSWITH "com.apple.Accessibility")' >"$1" 2>&1 &
	log_pid=$!
	log_since=$(date '+%Y-%m-%d %H:%M:%S')
	sleep 2
}

log_stop() {
	[ -z "$log_pid" ] || { sleep 2; kill "$log_pid" 2>/dev/null || true; wait "$log_pid" 2>/dev/null || true; }
	log_pid=
}
