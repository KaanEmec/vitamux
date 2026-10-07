#!/usr/bin/env bash
# Redaction audit (J13.6): a real `vitamux serve` on a throwaway database, configured with
# sentinel secrets (owner password, Withings client secret entered through the API, a generated
# panel sidecar secret, AI provider keys, master key), is driven by tools/redactaudit through
# sign-in, TOTP, API keys, source setup, push ingestion with sentinel health values, a failing
# Withings verify and token exchange and a failing AI extraction (dead proxy and
# a closed local port, so nothing leaves the host), a fake extraction and an export. Then every
# log line, the /metrics output, audit_events, jobs and job_runs are searched for every
# sentinel, and the whole database and the export archive for every secret: zero hits required.
# Needs: VITAMUX_DATABASE_URL (superuser, as in .env.example and CI), psql, curl, go.
# Run: make redaction-audit
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/stack-lib.sh

db=vitamux_redaction
addr=127.0.0.1:18081
metrics=127.0.0.1:19091
stack_setup
go build -o "$work/redactaudit" ./tools/redactaudit
go run ./tools/fixturegen labpdf -out "$work/lab" -truth "$work/lab-truth" >/dev/null

mkdir -p "$work/secrets" "$work/scan/db"
password=$(sentinel password)
for name in withings gemini openai; do sentinel "$name" >"$work/secrets/$name"; done
{ echo "$password"; for f in "$work/secrets/"* "$VITAMUX_MASTER_KEY_FILE"; do tr -d '\n' <"$f"; echo; done; } >"$work/scan/secrets.txt"
stack_owner audit-owner "$password"

export VITAMUX_LOG_LEVEL=debug VITAMUX_METRICS_ADDR=$metrics \
	VITAMUX_GEMINI_MODEL=audit-model VITAMUX_GEMINI_API_KEY_FILE=$work/secrets/gemini \
	VITAMUX_OPENAI_COMPATIBLE_BASE_URL=http://127.0.0.1:9/v1 VITAMUX_OPENAI_COMPATIBLE_MODEL=audit-model \
	VITAMUX_OPENAI_COMPATIBLE_API_KEY_FILE=$work/secrets/openai VITAMUX_OPENAI_COMPATIBLE_ALLOW_PRIVATE=true
HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 stack_serve # provider calls die here

REDACTION_AUDIT_PASSWORD=$password REDACTION_AUDIT_WITHINGS_SECRET=$(cat "$work/secrets/withings") "$work/redactaudit" -url "http://$addr" -user audit-owner \
	-lab "$work/lab" -out "$work/scan" -ai-model audit-model || { echo "server log:"; cat "$work/server.log"; exit 1; }
curl -fsS "http://$metrics/metrics" >"$work/scan/metrics.txt"
stack_stop
for t in $(psql "$url" -Atc "SELECT tablename FROM pg_tables WHERE schemaname = 'vitamux'"); do
	psql "$url" -qc "\\copy vitamux.$t TO '$work/scan/db/$t.tsv'"
done

# Positive controls: the scan sees real output, and the health sentinels did reach storage.
[ "$(wc -l <"$work/server.log")" -gt 20 ] || { echo "server log is nearly empty"; exit 1; }
grep -raqF -f "$work/scan/health.txt" "$work/scan/export" || { echo "health sentinels missing from the export"; exit 1; }

cd "$work"
logs=(*.log scan/metrics.txt scan/db/audit_events.tsv scan/db/jobs.tsv scan/db/job_runs.tsv)
hits=$( (grep -raFo -f scan/health.txt "${logs[@]}"; grep -raFo -f scan/secrets.txt "${logs[@]}" scan/db scan/export) | sort -u || true)
n=$(sort -u scan/secrets.txt scan/health.txt | wc -l | tr -d ' ')
if [ -n "$hits" ]; then
	printf 'redaction audit FAILED: sentinel found (file:sentinel)\n%s\n' "$hits"
	exit 1
fi
echo "redaction audit passed: $n sentinels, 0 occurrences in $(wc -l <server.log | tr -d ' ') log lines, metrics, audit, jobs, database and export"
