#!/usr/bin/env bash
# Performance budgets (J22.22): reads the app's `dashboardReady` and `chartRender` signposts
# (Sources/Shared/Signposts.swift) from a simulator's log since a time and checks each interval
# against its budget. PerformanceUITests drives them: the dashboard twice, and heart rate's Day
# view on the fake server's 14,400-row day at every zoom rung with both sources drawn. The
# simulator budgets are generous; a device's frame times are J22.23's.
#   apple/VitamuxApp/scripts/perf-signposts.sh UDID 'YYYY-MM-DD HH:MM:SS'
#   DASHBOARD_BUDGET_MS (1000)  CHART_BUDGET_MS (500)
set -euo pipefail
udid=$1 since=$2
out=$(mktemp)
trap 'rm -f "$out"' EXIT
xcrun simctl spawn "$udid" log show --start "$since" --signpost --style ndjson \
	--predicate 'subsystem == "org.vitamux.app" AND category == "PointsOfInterest"' >"$out" 2>/dev/null
python3 - "$out" "${DASHBOARD_BUDGET_MS:-1000}" "${CHART_BUDGET_MS:-500}" <<'PY'
import json, statistics, sys
from datetime import datetime

path, dash, chart = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
budgets = {"dashboardReady": dash, "chartRender": chart}
minimum = {"dashboardReady": 2, "chartRender": 4}
open_, spans = {}, {name: [] for name in budgets}
for line in open(path):
    if not line.startswith("{"):
        continue
    e = json.loads(line)
    name = e.get("signpostName")
    if e.get("eventType") != "signpostEvent" or name not in budgets:
        continue
    key = (e["processID"], name, e["signpostID"])
    at = datetime.strptime(e["timestamp"], "%Y-%m-%d %H:%M:%S.%f%z")
    if e["signpostType"] == "begin":
        open_[key] = (at, e.get("eventMessage", ""))
    elif key in open_:
        began, note = open_.pop(key)
        if e.get("eventMessage") == "superseded":
            continue  # a newer layer replaced it before it was drawn
        spans[name].append(((at - began).total_seconds() * 1000, note))
failed = False
for name, budget in budgets.items():
    ms = [d for d, _ in spans[name]]
    if len(ms) < minimum[name]:
        print(f"{name}: {len(ms)} intervals, want at least {minimum[name]} (did PerformanceUITests run?)")
        failed = True
        continue
    worst = max(spans[name])
    over = [f"{d:.0f} ms ({n})" if n else f"{d:.0f} ms" for d, n in spans[name] if d > budget]
    print(f"{name}: n={len(ms)} median={statistics.median(ms):.0f} ms max={worst[0]:.0f} ms {worst[1]} budget={budget:.0f} ms" + (f"  OVER: {', '.join(over)}" if over else ""))
    failed |= bool(over)
sys.exit(1 if failed else 0)
PY
