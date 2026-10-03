#!/usr/bin/env bash
# Real-stack E2E smoke (web/e2e-stack): builds the server with the embedded SPA, runs it on a
# throwaway database with a generated owner, runs `playwright --project=stack`, cleans up.
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
stack_serve

cd web
VITAMUX_E2E_STACK_URL=http://$addr npx playwright test --project=stack || { echo "server log:"; cat "$work/server.log"; exit 1; }
