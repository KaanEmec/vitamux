#!/usr/bin/env bash
# Checks one sidecar (docs/sidecars.md#checks): license gate, unit tests on synthetic cassettes
# (the Dockerfile's `test` stage), the image in replay mode against `vitamux connector-test`, and
# the Go normalizer goldens. Used by .github/workflows/sidecar-ci.yml and sidecar-canary.yml.
#
#   scripts/sidecar-check.sh <name> [upstream-git-source]
#
# With a git source (e.g. git+https://github.com/owner/repo) the sidecar is built against that
# instead of the locked registry release: the nightly canary. Needs docker, go and curl.
set -euo pipefail
cd "$(dirname "$0")/.."
name=${1:?usage: scripts/sidecar-check.sh <name> [upstream-git-source]}
src=${2:-}
dir=sidecars/$name
[[ $name =~ ^[a-z][a-z0-9_-]*$ && -d $dir ]] || { echo "no sidecar $dir" >&2; exit 1; }
image=vitamux-sidecar-$name:check

echo "== license gate"
go run ./tools/notices -sidecars-only

echo "== unit tests on cassettes"
docker build --target test --build-arg "UPSTREAM_GIT=$src" "$dir"

echo "== image"
docker build -t "$image" --build-arg "UPSTREAM_GIT=$src" "$dir"

echo "== connector-test in replay mode"
mkdir -p tmp # under the repo: Docker VMs on macOS share the home directory only
work=$(mktemp -d "$PWD/tmp/sidecar-check.XXXXXX")
cid=
cleanup() { [ -z "$cid" ] || docker rm -f "$cid" > /dev/null; rm -rf "$work"; }
trap cleanup EXIT
od -An -tx1 -N32 /dev/urandom | tr -d ' \n' > "$work/secret"
chmod 0644 "$work/secret" # the container user is not the runner
cid=$(docker run -d --read-only --tmpfs /tmp -e REPLAY=1 -e VITAMUX_SIDECAR_SECRET_FILE=/run/secret \
  -v "$work/secret:/run/secret:ro" -v "$PWD/$dir/testdata:/app/testdata:ro" -p 127.0.0.1::8080 "$image")
port=$(docker port "$cid" 8080/tcp | head -1 | sed 's/.*://')
for _ in $(seq 1 30); do
  nc -z 127.0.0.1 "$port" 2> /dev/null && break
  sleep 1
done
go run ./cmd/vitamux connector-test --url "http://127.0.0.1:$port" --secret-file "$work/secret"

echo "== Go normalizer goldens"
if [ -d "internal/connectors/${name//-/_}" ]; then
  go test "./internal/connectors/${name//-/_}/..."
else
  echo "no internal/connectors/${name//-/_}: skipped"
fi
echo "sidecar $name: all checks passed"
