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
| `withings/getmeas-*.json` | the Withings groups as getmeas responses (provider wire format; see [withings.md](../docs/providers/withings.md#fixtures)) |

A provider-shaped writer (Withings in J08.1, HealthKit in E15) implements `ShapeWriter` and renders the same world as raw wire format.

## Sources

Devices carry realistic manufacturers (`Apple Inc.` with model `Watch` or `iPhone`, `Garmin`, `Withings`) so brand selectors match. `garmin_watch` (6 s HR, 15 min steps + daily totals, resting HR, staged sleep, naps) · `apple_watch` (irregular HR, dense in workouts; variable step intervals; partial sleep) · `iphone` (steps while carried) · `garmin_via_healthkit` (the same watch relayed: minute HR, hourly steps; origin `com.garmin.connect.mobile`) · `withings_bp` (BP groups) · `withings_scale` (body composition groups) · `withings_via_healthkit` (relayed weigh-ins) · `manual_entry` (typed HR). About 6.1 M heart rate rows per year.

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

## Lab reports

```
go run ./tools/fixturegen labpdf     # -seed 42 -out fixtures/generated/lab -truth fixtures/lab
```

Twelve synthetic blood-test reports from invented labs and `SYNTHETIC PATIENT nn` placeholders (date of birth `0000-00-00`), with values drawn from the seed within the printed range, a few pushed outside it so printed flags appear. The PDFs (hand-written PDF 1.4, Helvetica text layer, `.synthetic` sidecars) go to the git-ignored `fixtures/generated/lab/`. Committed in `fixtures/lab/`: one ground truth per report in the extraction contract ([lab-documents#extracted-row-schema-v1](../docs/architecture/lab-documents.md#extracted-row-schema-v1)) and `manifest.json` with each PDF's sha256, layout, features and the [analyte-catalog](../docs/architecture/analyte-catalog.md) code per row (`null` = unknown analyte). `tools/fixturegen/labpdf_test.go` regenerates and compares, so the manifest pins the PDFs. `fixtures/lab/lab.go` embeds the ground truth for the `fake` extractor, which answers by PDF sha256.

Coverage: table, inline, stacked and two-panel layouts; Letter and A4; mg/dL and SI units, HbA1c in % and mmol/mol, Lp(a) in mg/dL and nmol/L, D-dimer in µg/mL FEU; comparators `<`, `>`, `≤` in results and ranges; upper- or lower-only ranges; German labels with decimal comma; US, ISO, German and ambiguous `dd/mm` dates; qualitative results and a titre; row-level specimen; multi-page (2 and 3 pages); `lab-10` is scanned (1-bit image, no text layer) with one smudged, unreadable result; unknown analytes in `lab-04`, `lab-08`, `lab-11`.

## Chart grammar

`chart-grammar.json` (hand-written, catalogue metadata only): `GET /metrics` entries with the chart view and Day-view bucket each should get. Checked by the panel (`web/e2e/chart-grammar.spec.ts`) and VitamuxKit (`ChartsTests`), so the two grammars cannot drift ([J22.6](../docs/plan/E22-ios-app/J22.6-chart-kit.md)).

## Rule model

`rule-model.json` (generated from the panel's `web/src/lib/rules/rule.ts` and `sentence.ts`, catalogue-free): builder forms, the exact rule JSON each saves and its plain sentence, including every rule in `internal/resolve/testdata/valid`. Checked by the panel (`web/e2e/rule-model.spec.ts`) and VitamuxKit (`RuleModelTests`), so a rule saved on the phone is byte-identical to the same rule saved in the panel ([J22.10](../docs/plan/E22-ios-app/J22.10-rules.md)).
