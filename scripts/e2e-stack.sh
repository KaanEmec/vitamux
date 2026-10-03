#!/usr/bin/env bash
# Real-stack E2E smoke (web/e2e-stack): builds the server with the embedded SPA, runs it on a
# throwaway database with a generated owner, runs `playwright --project=stack`, cleans up.
# Needs: VITAMUX_DATABASE_URL (superuser, as in .env.example and CI), psql, curl, go, node,
# and a built SPA (make web-build). Run: make test-e2e-stack
set -euo pipefail
cd "$(dirname "$0")/.."
: "${VITAMUX_DATABASE_URL:?set VITAMUX_DATABASE_URL (see .env.example)}"

admin=$VITAMUX_DATABASE_URL
db=vitamux_e2e
url=$(printf %s "$admin" | sed -E "s#(://[^/]+)/[^?]*#\1/$db#")
addr=127.0.0.1:18080
work=$(mktemp -d)
server=
cleanup() {
	[ -z "$server" ] || kill "$server" 2>/dev/null || true
	psql "$admin" -qc "DROP DATABASE IF EXISTS $db WITH (FORCE)" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

psql "$admin" -v ON_ERROR_STOP=1 -qc "DROP DATABASE IF EXISTS $db WITH (FORCE)" -c "CREATE DATABASE $db"
psql "$url" -v ON_ERROR_STOP=1 -q -f deploy/sql/roles.sql
CGO_ENABLED=0 go build -tags webui -o "$work/vitamux" ./cmd/vitamux

export VITAMUX_ENV=development VITAMUX_DATABASE_URL=$url VITAMUX_DATABASE_URL_FILE= \
	VITAMUX_HTTP_ADDR=$addr VITAMUX_PUBLIC_URL=http://$addr VITAMUX_DATA_DIR=$work/data \
	VITAMUX_MASTER_KEY_FILE=$work/data/master.key VITAMUX_LOG_LEVEL=warn
"$work/vitamux" migrate up >/dev/null
"$work/vitamux" admin init-secrets >/dev/null
export VITAMUX_E2E_USER=e2e-owner VITAMUX_E2E_PASSWORD
VITAMUX_E2E_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')
printf '%s\n%s\n' "$VITAMUX_E2E_USER" "$VITAMUX_E2E_PASSWORD" | "$work/vitamux" admin create-owner >/dev/null

"$work/vitamux" serve 2>"$work/server.log" &
server=$!
for _ in $(seq 60); do curl -fs "http://$addr/readyz" >/dev/null && break; sleep 0.5; done
curl -fs "http://$addr/readyz" >/dev/null || { echo "server not ready:"; cat "$work/server.log"; exit 1; }

cd web
VITAMUX_E2E_STACK_URL=http://$addr npx playwright test --project=stack || { echo "server log:"; cat "$work/server.log"; exit 1; }
