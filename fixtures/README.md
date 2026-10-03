---
synthetic: true
---

# Synthetic fixtures

Everything here is fictional and produced by [`tools/fixturegen`](../tools/fixturegen). One persona (Europe/Berlin, with one trip to New York), one year (2025), eight sources. Policy: [project#synthetic-fixtures-policy](../docs/architecture/project.md#synthetic-fixtures-policy).

```
make fixtures SEED=42        # writes fixtures/generated/ (about 0.8 GB, git-ignored, ~2 s)
go run ./tools/fixturegen -start 2025-03-28 -days 4 -hr-step 60 -out /tmp/x   # any slice
```

The output is canonical truth: what normalization should end up with, using only `v1` codes of [metrics.md](../docs/metrics.md). It is byte-identical for the same flags on every platform (integer arithmetic only, embedded tzdata, seeded PCG streams keyed by day, so a slice equals the same days of the full year). Nothing under `fixtures/generated/` is committed; `tools/fixturegen/main_test.go` pins digests instead. A generated file starts with `{"record":"header","synthetic": true,...}`.

| File | Records |
| --- | --- |
| `manifest.json` | seed, window, record counts |
| `sources.ndjson` | the eight (provider, device, origin) streams |
| `measurements.ndjson` | samples, intervals, daily values (`ext` = upstream id, `flags` = `relayed` / `manual_entry`) |
| `groups.ndjson` | `bp_reading` and `body_composition` groups with their component values |
| `sleep.ndjson` | sessions with stages; `null` totals mean the source does not report them |
| `revisions.ndjson` | later corrections and deletions of earlier records (originals stay in the other files) |

A provider-shaped writer (Withings in J08.1, HealthKit in E15) implements `ShapeWriter` and renders the same world as raw wire format.

## Sources

`garmin_watch` (6 s HR, 15 min steps + daily totals, resting HR, staged sleep, naps) · `apple_watch` (irregular HR, dense in workouts; variable step intervals; partial sleep) · `iphone` (steps while carried) · `garmin_via_healthkit` (the same watch relayed: minute HR, hourly steps; origin `com.garmin.connect.mobile`) · `withings_bp` (BP groups) · `withings_scale` (body composition groups) · `withings_via_healthkit` (relayed weigh-ins) · `manual_entry` (typed HR). About 6.1 M heart rate rows per year.

## Scenarios and resolution edge cases

Numbers refer to [resolution#edge-cases](../docs/architecture/resolution.md#edge-cases). Each scenario is pinned to a date, so it appears in a slice only if the slice contains that date.

| # | Edge case | Where it lives |
| --- | --- | --- |
| 1 | Daily total next to intraday intervals | `garmin_watch` `steps`: 15 min `interval` rows and a `daily_value` (`garmin-steps-<date>`) every day |
| 2 | Provider relayed through Apple Health and also direct | `garmin_via_healthkit` (flag `relayed`, relayed provider `garmin`) beside `garmin_watch` |
| 3 | Cross-source sum needs acknowledgement | `apple_watch` + `iphone` steps overlap by design; summing doubles them |
| 4 | Additive day from hourly maxima | the three step sources have different hourly coverage (iPhone is carried about 55 % of the time) |
| 5 | Intervals crossing a window boundary | `apple_watch` and `iphone` step intervals are 5-40 min with no alignment |
| 6 | Missing sleep stages | `apple_watch` nights with `has_stages: false`, one `asleep_unspecified` stage, `deep_s: null` |
| 7 | Split night | `garmin_watch`: wake of 75 min on 2025-03-12, 05-27, 09-09, 11-18 (separate episodes); 35 min on 04-08, 07-22, 10-14 (merge); Apple Watch records those nights whole |
| 8 | Partial wear below coverage | `garmin_watch` off 13:00-21:00 on 2025-06-03, 06-04, 06-10; a daily 30-60 min charging gap; Apple Watch off 06:30-07:30 |
| 9 | Fallback per window | `garmin_watch` dead 2025-02-17 and 02-18 (relay and Apple data remain); Apple Watch off 2025-08-04..10 (iPhone steps only) |
| 10 | Different metrics never pooled | not covered here: v1 has one heart rate variability sample code per method and no generator uses them |
| 11 | Manual entries excluded by default | `manual_entry` heart rate every 11th day, flag `manual_entry` |
| 12 | Late or corrected data | `revisions.ndjson`: Garmin step totals for 2025-04-14..16 corrected two days later; `bp_systolic` of 2025-02-11 corrected; the 2025-07-09 evening BP group and the 2025-09-03 weigh-in deleted upstream |
| 13 | DST and travel | spring forward 2025-03-30, fall back 2025-10-26; New York 2025-05-12..21 (offsets change, flights remove the first and last night, a timezone change never overlaps in time) |
| 14 | Duplicate weigh-in via relay | `withings_scale` group plus `withings_via_healthkit` `weight` and `body_fat_ratio` at the same instant on about 60 % of weigh-ins |
| 15 | Today is partial | any slice ending mid-year; no special data |

Also covered: high-frequency (6 s) and minute heart rate, irregular watch samples, gaps, BP twice a day, weigh-ins about every other day, naps.
