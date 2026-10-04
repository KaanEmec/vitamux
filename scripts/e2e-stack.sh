#!/usr/bin/env bash
# Real-stack E2E smoke (web/e2e-stack): builds the server with the embedded SPA, runs it on a
# throwaway database with a generated owner and synthetic origins, runs `playwright --project=stack`, cleans up.
# Needs: VITAMUX_DATABASE_URL (superuser, as in .env.example and CI), psql, curl, go, node,
# and a built SPA (make web-build). Run: make test-e2e-stack
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/stack-lib.sh

db=vitamux_e2e
addr=127.0.0.1:18080
stack_setup webui
export VITAMUX_E2E_USER=e2e-owner VITAMUX_E2E_PASSWORD
VITAMUX_E2E_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')
stack_owner "$VITAMUX_E2E_USER" "$VITAMUX_E2E_PASSWORD"
# Synthetic origins and a watch for the owner; the device specs pair a device and classify them.
psql "$url" -v ON_ERROR_STOP=1 -q <<'SQL'
INSERT INTO data_origins (id, user_id, provider_id, origin_key, name, is_native)
SELECT gen_random_uuid(), u.id, p.id, v.key, v.name, v.native
FROM users u, providers p,
  (VALUES ('com.example.synthetic.garmin', 'Synthetic Garmin app', false), ('com.apple.health.synthetic', 'Synthetic Apple origin', true)) AS v (key, name, native)
WHERE p.code = 'apple_health';
INSERT INTO devices (id, user_id, provider_id, fingerprint, device_type, manufacturer, model)
SELECT gen_random_uuid(), u.id, p.id, 'synthetic-watch', 'watch', 'Synthetic', 'Watch 1' FROM users u, providers p WHERE p.code = 'apple_health';
SQL
stack_serve

cd web
VITAMUX_E2E_STACK_URL=http://$addr npx playwright test --project=stack || { echo "server log:"; cat "$work/server.log"; exit 1; }
