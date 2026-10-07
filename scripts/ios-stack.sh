#!/usr/bin/env bash
# iOS real-stack smoke (J22.22): a real `vitamux serve` on a throwaway database with a synthetic
# fixturegen week ending today (tools/fixtureload), an owner with two-factor on and a paused fake
# Withings connection; then StackSmokeUITests on a throwaway simulator with the app pointed at
# the server through a launch argument: server step, sign-in with TOTP, dashboard, a metric, an
# override, the Withings connection, sign-out. The app's unified log is scanned for the run's
# sentinels (password, TOTP secret, override value and note) and `vmx_` tokens.
# Needs: VITAMUX_DATABASE_URL (superuser, as in .env.example), psql, curl, go, python3, Xcode,
# xcodegen. Run: make test-ios-stack (RESULT_BUNDLE, SIM_* as in apple/VitamuxApp/scripts/sim-lib.sh)
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
. scripts/stack-lib.sh

db=vitamux_ios_stack
addr=127.0.0.1:18082
stack_setup
. apple/VitamuxApp/scripts/sim-lib.sh
trap 'sim_cleanup; stack_cleanup' EXIT
bundle=${RESULT_BUNDLE:-$root/tmp/ios/stack-smoke.xcresult}
mkdir -p "$(dirname "$bundle")"

user=synthetic-owner # tools/fixtureload's owner
password=$(sentinel password)
note=$(sentinel note)
value=4$((RANDOM % 9)).$((1000 + RANDOM % 9000))

echo "== synthetic week, owner and a paused Withings connection"
from=$(TZ=Europe/Berlin date -v-6d +%F)
go run ./tools/fixturegen -start "$from" -days 7 -hr-step 60 -out "$work/fixtures" >/dev/null
go build -o "$work/fixtureload" ./tools/fixtureload
"$work/fixtureload" "$work/fixtures" >>"$work/cli.log"
printf '%s\n%s\n' "$user" "$password" | stack_cli admin reset-password
withings=$(psql "$url" -v ON_ERROR_STOP=1 -qAt <<'SQL'
INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
SELECT gen_random_uuid(), u.id, p.id, sha256('synthetic-withings-account'::bytea), 'in_process', 'paused'
FROM users u, providers p WHERE p.code = 'withings'
RETURNING 'conn_' || replace(id::text, '-', '');
SQL
)
stack_serve

echo "== two-factor on for the owner"
base=http://$addr
jar=$work/cookies
csrf=$(curl -fsS -c "$jar" -H 'Content-Type: application/json' \
	-d "$(jq -n --arg u "$user" --arg p "$password" '{username: $u, password: $p}')" "$base/api/v1/auth/login" | jq -r .csrf_token)
secret=$(curl -fsS -b "$jar" -H "X-CSRF-Token: $csrf" -X POST "$base/api/v1/auth/totp/enroll" | jq -r .secret)
code=$(python3 - "$secret" <<'PY'
import base64, hmac, struct, sys, time
key = base64.b32decode(sys.argv[1] + "=" * (-len(sys.argv[1]) % 8))
mac = hmac.new(key, struct.pack(">Q", int(time.time()) // 30), "sha1").digest()
o = mac[-1] & 15
print("%06d" % ((struct.unpack(">I", mac[o:o + 4])[0] & 0x7FFFFFFF) % 1000000))
PY
)
curl -fsS -b "$jar" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d "{\"code\":\"$code\"}" \
	"$base/api/v1/auth/totp/confirm" | jq -r '.recovery_codes[]' >"$work/recovery"
curl -fsS -b "$jar" -H "X-CSRF-Token: $csrf" -X POST "$base/api/v1/auth/logout" >/dev/null

sim_setup
# The shipped app keeps its server and session: start from nothing on a reused simulator too.
xcrun simctl uninstall "$udid" org.vitamux.healthbridge 2>/dev/null || true
xcrun simctl keychain "$udid" reset
app_build
printf '%s\n' "$password" "$secret" "$note" "$value" "${value/./,}" >"$work/markers"
cat "$work/recovery" >>"$work/markers"

echo "== StackSmokeUITests against $base"
log_start "$work/unified.log"
rc=0
TEST_RUNNER_VITAMUX_STACK_URL=$base TEST_RUNNER_VITAMUX_STACK_USER=$user TEST_RUNNER_VITAMUX_STACK_PASSWORD=$password \
	TEST_RUNNER_VITAMUX_STACK_TOTP_SECRET=$secret TEST_RUNNER_VITAMUX_STACK_WITHINGS=$withings \
	TEST_RUNNER_VITAMUX_STACK_VALUE=$value TEST_RUNNER_VITAMUX_STACK_NOTE=$note \
	app_test "$bundle" -only-testing:VitamuxUITests/StackSmokeUITests || rc=$?
log_stop
grep -q "StackSmokeUITests testServerSignInDashboardMetricOverrideWithingsSignOut passed" "$work/xcodebuild.log" ||
	grep -q "testServerSignInDashboardMetricOverrideWithingsSignOut\]' passed" "$work/xcodebuild.log" || {
	echo "the stack smoke did not pass (skipped or failed); server log:"; tail -40 "$work/server.log"; exit 1; }
[ "$rc" -eq 0 ] || exit "$rc"
psql "$url" -qAt -c "SELECT count(*) FROM manual_overrides" | grep -qx 1 || { echo "no override stored on the server"; exit 1; }
echo "== privacy log scan"
apple/VitamuxApp/scripts/log-scan.sh "$work/unified.log" "$work/markers"
echo "iOS stack smoke passed"
