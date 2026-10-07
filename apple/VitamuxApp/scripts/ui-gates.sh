#!/usr/bin/env bash
# The app's simulator gates (J22.22), run by CI's swift job and `make test-ios`: builds the app
# and the widget for the simulator, runs the UI suite on the fake server and the widget tests
# into a result bundle while capturing the unified log, then the privacy scan (the fake's
# synthetic secrets and any `vmx_` token, scripts/log-scan.sh), the performance budgets
# (scripts/perf-signposts.sh) and the privacy-manifest validation. Accessibility audits are part
# of the suite (AccessibilityAuditUITests). Extra arguments go to xcodebuild, e.g.
# `-only-testing:VitamuxUITests/PerformanceUITests` (the performance check needs that class).
#   apple/VitamuxApp/scripts/ui-gates.sh [xcodebuild args]
#   RESULT_BUNDLE (default tmp/ios/ui-tests.xcresult at the repository root; the performance run
#   lands next to it as ui-tests-performance.xcresult); sim-lib.sh's SIM_*
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(git -C "$here" rev-parse --show-toplevel)
work=$(mktemp -d)
. "$here/sim-lib.sh"
trap 'sim_cleanup; rm -rf "$work"' EXIT
bundle=${RESULT_BUNDLE:-$root/tmp/ios/ui-tests.xcresult}
mkdir -p "$(dirname "$bundle")"

sim_setup
app_build
# ADR-0023: the app resolves exactly VitamuxKit's pinned packages (CI checks that list), nothing of its own.
diff <(ls "$work/derived/SourcePackages/checkouts" | tr '[:upper:]' '[:lower:]' | sort) \
	<(jq -r '.pins[].identity' "$app_dir/../VitamuxKit/Package.resolved" | sort) ||
	{ echo "the app resolves packages beyond VitamuxKit's"; exit 1; }
"$here/privacy-manifest.sh" "$work/derived/Build/Products/Debug-iphonesimulator/Vitamux.app"

# The fake server's synthetic secrets (VitamuxKit FakeServer*.swift, UITests) and the note the
# override tests type: none may reach the log.
cat >"$work/markers" <<'MARKERS'
synthetic-password
synthetic-new-password
synthetic-recovery-
synthetic-secret
synthetic_secret
synthetic-client-secret
synthetic-device-token
synthetic note
JBSWY3DPEHPK3PXP
otpauth://
MARKERS

echo "== UI suite and widget tests on the fake server (log captured)"
log_start "$work/unified.log"
rc=0
if [ $# -eq 0 ]; then
	app_test "$bundle" -skip-testing:VitamuxUITests/PerformanceUITests || rc=$?
else
	app_test "$bundle" "$@" || rc=$?
fi
log_stop
echo "== privacy log scan"
"$here/log-scan.sh" "$work/unified.log" "$work/markers" || rc=1
# The performance run on its own, after the capture: a debug-level stream and a long suite push
# the signposts out of the simulator's in-memory log before they can be read.
if [ $# -eq 0 ] || grep -q PerformanceUITests <<<"$*"; then
	echo "== performance run"
	since=$(date '+%Y-%m-%d %H:%M:%S')
	app_test "${bundle%.xcresult}-performance.xcresult" -only-testing:VitamuxUITests/PerformanceUITests || rc=1
	"$here/perf-signposts.sh" "$udid" "$since" || rc=1
fi
exit "$rc"
