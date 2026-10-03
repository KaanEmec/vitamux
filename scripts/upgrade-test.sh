#!/usr/bin/env bash
# Upgrade test (J14.4): runs the release Compose stack on OLD_IMAGE, loads a five-day
# fixturegen slice and takes a backup, then does the documented upgrade to NEW_IMAGE
# (`docker compose up -d --wait`, whose one-shot `migrate` runs `migrate up` first) and checks
# the schema version, the running version, row counts, `resolve verify` and /readyz.
#
#   OLD_IMAGE=ghcr.io/kaanemec/vitamux:v0.1.0 NEW_IMAGE=vitamux:dev scripts/upgrade-test.sh
#
# OLD_SRC is a checkout of the old version (default: this tree). Its fixturegen and fixtureload
# write the slice, so the rows match the old schema; the Compose files come from this tree.
# Needs docker (Compose v2), go and curl. Synthetic data only, in a throwaway Compose project
# whose volumes are removed on exit. Not sourced from stack-lib.sh: that one runs a host binary
# against a dev database, this one runs images in the release stack.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${OLD_IMAGE:?set OLD_IMAGE (the previous release image)}" "${NEW_IMAGE:?set NEW_IMAGE (the candidate image)}"
old_src=$(cd "${OLD_SRC:-.}" && pwd)
port=${UPGRADE_TEST_PORT:-18090}
project=vitamux-upgrade-test
from=2025-02-15
to=2025-02-19
tables="users connections devices data_origins ingest_batches blobs raw_payloads measurements measurement_groups sleep_sessions sleep_stages timezone_periods"

# Under the repo (git-ignored tmp/), not /tmp: Docker VMs on macOS (Colima, Docker Desktop)
# share the home directory, and bind mounts of unshared paths silently come up empty.
mkdir -p tmp
work=$(mktemp -d "$PWD/tmp/upgrade-test.XXXXXX")
compose_dir=$work/deploy/compose
dc() { docker compose -p "$project" -f "$compose_dir/compose.yaml" "$@"; }
sql() { dc exec -T postgres psql -U vitamux_admin -d vitamux -v ON_ERROR_STOP=1 -Atc "$1"; }
step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() {
	rc=$?
	[ "$rc" -eq 0 ] || dc logs --no-color --tail 80 2>/dev/null || true # secret-free by design (redaction audit)
	dc down -v --remove-orphans >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$rc"
}
trap cleanup EXIT

counts() {
	local q="" t
	for t in $tables; do q+="SELECT '$t', count(*) FROM vitamux.$t UNION ALL "; done
	sql "${q% UNION ALL }"
}

step "release Compose files (the bundle layout) into a temporary directory"
mkdir -p "$compose_dir/initdb" "$work/deploy/sql"
cp deploy/compose/compose.yaml deploy/compose/.env.example deploy/compose/init-secrets.sh "$compose_dir/"
cp deploy/compose/initdb/*.sql "$compose_dir/initdb/"
cp deploy/sql/roles.sql "$work/deploy/sql/"
cp deploy/compose/.env.example "$compose_dir/.env"
echo "VITAMUX_PORT=$port" >>"$compose_dir/.env"

step "fixture slice $from..$to and loader from $old_src"
arch=$(docker version --format '{{.Server.Arch}}')
loader_src=$old_src
[ -d "$old_src/tools/fixtureload" ] || { echo "OLD_SRC has no tools/fixtureload; using this tree's"; loader_src=$PWD; }
(cd "$old_src" && go run ./tools/fixturegen -out "$work/fixtures" -start "$from" -days 5 -hr-step 60 >/dev/null)
(cd "$loader_src" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/fixtureload" ./tools/fixtureload)
chmod -R a+rX "$work/fixtures" "$work/fixtureload" # read by the container's non-root user

step "old stack on $OLD_IMAGE"
export VITAMUX_IMAGE=$OLD_IMAGE
"$compose_dir/init-secrets.sh" >/dev/null
dc --profile setup run --rm init-secrets
dc up -d --wait postgres
dc run --rm migrate
dc run --rm --no-deps -v "$work/fixtures:/fixtures:ro" -v "$work/fixtureload:/fixtureload:ro" \
	--entrypoint /fixtureload vitamux /fixtures
dc up -d --wait
curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null || fail "old stack not ready"
dc exec -T vitamux /vitamux version

step "backup before the upgrade"
dc exec -T vitamux /vitamux backup
before=$(counts)
echo "$before" | tr '\n' ' '
echo
[ "$(echo "$before" | awk -F'|' '$1 == "measurements" {print $2}')" -gt 0 ] || fail "no measurements loaded"

step "upgrade to $NEW_IMAGE (docker compose up -d --wait: migrate up, then vitamux)"
export VITAMUX_IMAGE=$NEW_IMAGE
dc up -d --wait
[ "$(dc ps -a --format '{{.ExitCode}}' migrate)" = 0 ] || fail "migrate did not exit 0"

step "checks"
want_version=$(docker run --rm "$NEW_IMAGE" version)
got_version=$(dc exec -T vitamux /vitamux version)
[ "$got_version" = "$want_version" ] || fail "running '$got_version', want '$want_version'"
want_schema=$(echo "$want_version" | sed -E 's/.*schema ([0-9]+).*/\1/')
got_schema=$(sql "SELECT coalesce(max(version_id), 0) FROM vitamux.goose_db_version")
[ "$got_schema" = "$want_schema" ] || fail "schema $got_schema, want $want_schema"
echo "schema $got_schema: ok"
after=$(counts)
[ "$after" = "$before" ] || fail "row counts changed: before [$(echo "$before" | tr '\n' ' ')] after [$(echo "$after" | tr '\n' ' ')]"
echo "row counts: unchanged"
dc exec -T vitamux /vitamux resolve verify --from "$from" --to "$to" --windows 200 --seed 1
curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null || fail "new stack not ready"
echo "readyz: ok"
step "upgrade test passed ($OLD_IMAGE -> $NEW_IMAGE)"
