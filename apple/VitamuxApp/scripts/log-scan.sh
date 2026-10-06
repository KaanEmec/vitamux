#!/usr/bin/env bash
# Privacy log scan (J22.22): fails if LOG (a unified-log capture of the app, sim-lib.sh's
# log_start) holds any marker of MARKERS (fixed strings, one per line: the synthetic secrets
# and health values the run typed or served) or any `vmx_` token. Positive control: the
# capture must hold at least 50 lines from the app, so an empty capture never passes.
#   apple/VitamuxApp/scripts/log-scan.sh LOG MARKERS
set -euo pipefail
log=$1 markers=$2
lines=$(grep -c 'Vitamux' "$log" || true)
[ "$lines" -ge 50 ] || { echo "privacy scan: only $lines app lines captured; the log stream did not work"; exit 1; }
{ cat "$markers"; echo 'vmx_'; } | grep -v '^$' >"$log.markers"
hits=$(grep -aoF -f "$log.markers" "$log" | sort | uniq -c || true)
if [ -n "$hits" ]; then
	echo "privacy scan FAILED: markers in the unified log (count marker):"
	echo "$hits"
	grep -aF -f "$log.markers" "$log" | head -5 | cut -c1-300
	exit 1
fi
echo "privacy scan passed: $(wc -l <"$log.markers" | tr -d ' ') markers, 0 occurrences in $lines app log lines"
