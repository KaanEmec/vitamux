# Resolution

Resolution turns many source rows into one explained value per (metric, window). It only reads canonical rows ([data-model.md](data-model.md)) and only writes rebuildable cache.

## Rule specification

A rule is stored as JSONB in `resolution_rules.spec` and validated by `schemas/resolution-rule.v1.json` plus catalogue checks in `internal/resolve` ([ADR-0008](../adr/0008-rule-schema.md)). There is no expression language. `metric` is a catalogue code or a rule family (`sleep`, `blood_pressure`; see [group-coherent selection](#group-coherent-selection)).

```yaml
schema: vitamux.rule/1
metric: heart_rate
window: {kind: bucket, size: 5m}   # bucket|hour|local_day|local_night|sleep_episode|latest|reading
groups:                              # ordered; each input goes to the FIRST matching group
  - {id: garmin, match: [{provider: garmin}]}
  - {id: whoop,  match: [{provider: whoop}]}
  - {id: apple_watch, match: [{provider: apple_health, device_type: watch, relayed: false}]}
exclude: [{provider: apple_health, relayed: true}]
within_source: {intra_group: auto, daily_value_policy: prefer_reported}
strategy: {op: mean_across_sources, min_sources: 1, on_insufficient: use_available}
quality:
  min_coverage: 0.5
  plausible_range: [25, 230]
  exclude_flags: [manual_entry]
  max_staleness: 36h
  sleep: {match_overlap: 0.5, min_episode_coverage: 0.7, include_naps: false, night_anchor: "18:00"}
acknowledged_warnings: []
```

The provider names are illustrative; rules work for any provider.

## Selectors and validation

- Selector fields: `provider`, `connection_id`, `origin_key`, `origin_key_prefix`, `origin_name`, `relayed`, `device_type`, `device_model`, `device_manufacturer` (the brand, case-insensitive), `device_id`, `entry` (`device|manual`). Fields within one selector are ANDed; a list of selectors is ORed. `exclude` wins over every group; contexts (E1) reorder groups but never change membership.
- Comparisons are exact except `origin_key_prefix` (a prefix) and `device_manufacturer` (case-insensitive). A set field never matches a row without that value, so a brand or model selector skips rows without a device.
- Named choices ([J09.11](../plan/E09-resolution/J09.11-brand-device-selectors.md)): the rule builder offers presets built from the owner's own devices (`GET /source-devices`) and providers, and the built-ins use the same groups ([defaults](resolution-defaults.md#how-brands-are-selected)):

  | Choice | Selector |
  | --- | --- |
  | Apple Health (all data) | `{provider: apple_health}` |
  | Apple Watch / iPhone | `{provider: apple_health, device_manufacturer: "Apple Inc.", device_model: Watch}` / `… iPhone` |
  | Garmin Connect (all data) | `{provider: garmin}` |
  | Garmin (any device), direct and through Apple Health | `{device_manufacturer: Garmin}` |
  | One model | `{device_manufacturer: Garmin, device_model: "Forerunner 965"}` |
  | One named device | `{device_id: <id>}` |

  `relayed: false` still narrows a choice. Merged devices are not offered, because their records live on the target device.
- Validation rejects:
  - unknown fields;
  - windows or strategies not allowed for the metric's aggregation;
  - `single_source` with more than one group; empty groups;
  - `sum_across_sources` or `intra_group: sum` without `acknowledged_warnings: [cross_source_sum_duplicate_risk]`;
  - per-code rules for family members, and the extension checks listed in [ADR-0008](../adr/0008-rule-schema.md).
- Every edit creates an immutable version (`rule:<metric>:<n>`), and `active_rules` points to one; any version, older ones included, can be activated again. Activation runs `ValidateSet` on the whole active set and rejects only problems the change introduces. Built-in defaults live in code as `builtin:<metric>:<n>` ([list](../resolution-defaults.md)), apply while a metric has no active version, and are copied as version 1 on first edit. Creating and activating versions is audited with the actor and a field diff.

## Windows

| Kind | Definition (in the user's timezone for that date) |
| --- | --- |
| `bucket(size)` | 1, 5, 15, or 30 min, aligned to local midnight. DST follows the wall clock. Planned ([J22.26](../plan/E22-ios-app/J22.26-intraday-views.md)): 30 s for intensive metrics. |
| `hour` | Local hours. DST days have 23 or 25. |
| `local_day` | Rows with stored `local_date = D`, so a travel day is not split |
| `local_night` | Main sleep episode of night D: candidates end in `[D−1 anchor, D anchor)` ([ADR-0009](../adr/0009-sleep-date-night-window.md)) |
| `sleep_episode` | Every aligned episode, naps included |
| `latest` | Most recent valid input at or before `as_of` |
| `reading` | One result per measurement group (e.g., each BP reading) |

A window ending in the future returns `partial: true`.

## Within-source aggregation

This happens per source group, before any cross-source step, so dense sources do not dominate.

| Aggregation | Within-source value |
| --- | --- |
| intensive | Samples go into base buckets (`min(window, catalogue base_bucket)`). Bucket mean per group; several sub-sources in a group use the `intra_group` mean of their bucket means. Window value = mean of covered bucket means, **each bucket weighted equally**. Coverage = covered buckets / elapsed buckets. |
| additive | Intervals pro-rated linearly by overlap (flag `prorated`). For `local_day` with `prefer_reported`, use the provider `daily_value` if present, else the interval sum — **never both**. Sub-sources in a group use the per-bucket **max**, so iPhone + Watch steps are not added. With `require_wear` (E3), coverage = worn buckets / elapsed buckets. Wear-exempt groups (phones) and rules without the gate skip the coverage gate. |
| latest | Latest valid reading. Group metrics select whole groups. On `local_day`, `within_source.statistic` is `latest` (default) or `mean` of the selected readings, per component. |
| daily_summary | The source's `daily_value` for D, else the latest sample in D |
| sleep_derived | Sum over that group's sessions in the aligned main episode. Missing stages → `no_stage_data`, **not 0**. |

Example for one 5-min bucket: source A has 50 samples averaging 62.1, source B has 1 sample of 66. The mean across sources is (62.1 + 66) / 2 = **64.05**. A pooled-sample mean would be 62.18.

## Strategies

For each window, independently:

1. Assign inputs to groups. Excluded inputs are reported as such.
2. Compute each group's value and apply the quality gates: plausible range, flags, coverage, staleness, episode coverage, overrides. Group status becomes `valid | no_data | below_quality | excluded | stale | not_aligned | no_stage_data`. After the strategy, valid groups are `used` (with `selected` when their value is the result: every pooled group for mean and sum, the extreme one for minimum and maximum) or `fallback_unused`, and sources that match no group are listed as `not_in_rule`. Sleep episodes use the same statuses.
3. Apply the strategy:

| Op | Result | When inputs are insufficient |
| --- | --- | --- |
| `single_source` | The single group's value | `no_data` with reason |
| `first_available` | First valid group in order | Implicit fallback; skipped groups carry reasons |
| `mean_/minimum_/maximum_across_sources` | Over valid groups | Below `min_sources`: `use_available` (warning `insufficient_sources`) or `no_value` |
| `sum_across_sources` | Sum of valid groups | Only if acknowledged; the warning is repeated in every result |
| `latest` / `earliest` | Across groups; ties go by group order | — |
| `event_priority` | Whole event (session with stages, workout) from the first group with an aligned event | Stages are never spliced across sources |

   For selection-only metrics ([metric-catalog.md](metric-catalog.md#rules)), a selected group that differs from the previous window's adds warning `definition_changed`. The previous window is the one just before (the day before's last for the first window of a date); one that selected nothing gives no warning. A date's results therefore never depend on the requested range.
4. Apply overrides.
5. Emit the result.

## Sleep episode alignment

1. Gather candidate sessions for the night from the rule's groups (excluded and unmatched sessions are only listed). Merge same-source fragments that are ≤ 60 min apart.
2. Link sessions across sources when `overlap / min(duration_a, duration_b) ≥ match_overlap`. Connected components are episodes.
3. The main episode is the one with the largest union span. Others are secondary: `sleep_episode` windows resolve them; `local_night` adds them only with `include_naps`.
4. A group whose sessions cover less than `min_episode_coverage` of the episode is `below_quality: partial_episode`. When a group has several sources in the episode, the one covering most of it is used alone.
5. Codes sum the selected source's sessions: stored totals, with `sleep_latency` from the first session, `sleep_waso` and `sleep_unspecified` from stages, `sleep_in_bed` as session time, and `sleep_efficiency` = total / in bed.

Example: A 23:10–06:55 and B 23:40–07:05 overlap by 0.98, so they are matched and deep sleep is averaged. If A had only 03:00–07:00, its coverage would be 0.51 < 0.7, so A is excluded and B is used with warning `insufficient_sources`.

Workouts cluster the same way: overlap ≥ 0.6 of the shorter and a compatible sport (equal, or one is `other`; a cluster never holds two specific sports). Each cluster is listed once, with alternates; the picked workout keeps its own segments.

## Group-coherent selection

BP and body-composition components always come from the same reading. One `metric: blood_pressure` rule returns systolic, diastolic, and pulse together; body-composition codes use `follow: weight`. A daily mean (`within_source.statistic: mean`) averages each component across selected readings and never mixes sources within one reading.

Sleep codes are one family. A single rule (`metric: sleep`) selects one episode per window, and every `sleep_*` code reads from that episode. A selected source without stages gives `no_stage_data` for the stage codes, instead of falling through to another source.

## Extensions

Each extension stays typed (no expression language) and adds its inputs to the explanation. E1, E2, E3, E5 and E9 are part of rule schema v1 and built in [J09.10](../plan/E09-resolution/J09.10-rule-extensions.md) for the MVP. The others are proposed: until one exists, the plain rule applies. The [suggested defaults](resolution-defaults.md) mark where each helps.

| Id | Extension | Shape (sketch) | Main use | Status |
| --- | --- | --- | --- | --- |
| E1 | Context-specific ladders | `contexts: {workout: [group ids], sleep: [group ids]}`: inside aligned workouts or the sleep episode, the listed groups go first, then the rest in default order | HR, distance and energy during workouts; HR at night | v1 |
| E2 | Window statistics and derived codes | `within_source: {statistic: min_rolling_mean, span: 30m}` or `{statistic: min}`. Derived catalogue codes have no rows and resolve from a source metric with such a statistic | `resting_heart_rate_nocturnal` from `heart_rate`; `spo2_night_min` from `spo2` | v1 |
| E3 | Wear gate | `quality: {require_wear: heart_rate}`: a group counts in a bucket only if that device has HR samples there. Devices that never report the wear metric (phones) are exempt. Others get `below_quality: not_worn` | Telling "no steps" from "not worn" for the watch/phone/ring fallback | v1 |
| E4 | Coverage selection | `strategy: {op: max_coverage}`: the group with the highest coverage, ties by order | One energy source per day, chosen by wear | proposed |
| E5 | Cross-metric coherence | `follow: <metric>`: use the group the leader metric selected for the same window, else fall back with warning `follow_unavailable`. No cycles. Leader dirty marks also mark followers | Basal and total energy from the same source as active energy | v1 |
| E6 | Sticky selection | `stickiness: 14d`: switch to a new group only after that much continuous valid data | Baseline-dependent metrics (temperature deviation, HRV) | proposed |
| E7 | Multi-day windows | `window: {kind: days, size: 7}` plus a session filter (morning/evening, skip first day) | Home BP protocol average | proposed |
| E8 | Within-source earliest | `within_source: {pick: earliest}` for `latest`-type metrics | First morning weight | proposed |
| E9 | Hour composition | `compose: {from: hour, op: first_available\|max}` on `local_day` for additive metrics: resolve each hour, then sum the hours. A day made of hourly picks from different sources carries warning `composite_exceeds_any_source` when it exceeds every single source | Watch/phone/ring step fallback within a day | v1 |
| — | Median across sources | `strategy: {op: median_across_sources}` | Opt-in, three or more similar sources | proposed |

How the v1 extensions behave at the edges (`internal/resolve/extensions.go`):

- **E1:** `local_night` and `sleep_episode` windows are sleep windows; any other window is inside a workout or sleep episode when that event covers at least half of it (workouts first). `@workout_source` is the group of the workout the rule itself picks from the cluster.
- **E2:** `min` is the lowest base-bucket mean; `min_rolling_mean` the lowest mean of consecutive covered buckets spanning `span`, so a gap breaks a span (`below_quality: no_full_span`). The result names the span. Other window kinds keep the plain value.
- **E3:** a row part counts only in base buckets where its device (device row, else source) has a wear sample. A device without wear rows in the loaded series (callers load 30 days before the window) is exempt. A gated group worn in the window without rows is a measured 0, so "worn, no steps" never falls through to the next source; rows only in unworn buckets give `not_worn`.
- **E5:** a follower whose leader group is unusable falls back through its own ladder with status `fallback`. `follow` and `compose` cannot be combined.
- **E9:** hours resolve with their own context, wear and coverage gates and ignore daily values. The day lists every hour and each group's own day total (the sum of its hours), which is what `composite_exceeds_any_source` compares against.

## Manual overrides

`manual_overrides` are scoped to (metric, window kind, window key) and support three actions:

- `exclude_input`: a row, session, group, or whole source group for that window;
- `force_source(group)`;
- `set_value(value, unit, note)`: the result status becomes `overridden`, and the computed result is kept.

In the engine, `exclude_input` drops one input row (a whole reading for blood pressure) and resolves again; `force_source` selects the named group if it has a value; `set_value` wins over `force_source`. Each window result lists the applied and ignored overrides and keeps the computed result. A window left without a value stays `no_data`. At most one active `force_source` and one active `set_value` exist per window (revoke first); `set_value` is for single metrics, in the canonical unit.

Overrides never touch source rows. They are revocable (soft: the row stays as history) and audited without the value or note; each change marks that local date in `resolution_dirty`. Fixing a bad measurement means `exclude_input` or a `manual` provider entry.

## Result shape

```json
{"status": "fallback", "value": 52, "unit": "bpm", "partial": false,
 "window": {"kind": "local_day", "local_date": "2026-09-14"},
 "rule": {"ref": "builtin:resting_heart_rate", "version": 1, "strategy": "first_available"},
 "inputs": [
   {"group": "whoop", "status": "no_data", "reason": "stream degraded: schema_drift since 2026-09-13T06:00Z"},
   {"group": "garmin", "status": "used", "selected": true, "value": 52, "basis": "daily_value", "coverage": 1.0,
    "sources": [{"provider": "garmin", "connection_id": "conn_…", "device": {"type": "watch"}}], "record_refs": ["9182736"]}],
 "warnings": [{"code": "preferred_source_unavailable", "group": "whoop"}],
 "explanation": "WHOOP had no value (stream degraded). Fell back to Garmin: 52 bpm.",
 "computed_at": "2026-09-15T06:00:03Z"}
```

`status` ∈ `direct | fallback | calculated | overridden | no_data`. Every result lists every rule group, then each excluded or unmatched source. Explanations come from fixed templates (`internal/resolve/explain.go`, one per strategy, status and extension) that say what was computed from which source and never interpret it; snapshots live in `internal/resolve/testdata/explanations`. `resolve.Run` loads and resolves a (user, metric, window kind, date range); `resolve.BuildResult` renders one window.

## Edge cases

Each case is a scenario test (`internal/resolve/scenario_integration_test.go`, on `tools/fixturegen` slices; case 10 in memory, since the generator has no heart rate variability).

1. A daily total and intraday intervals from one source are never summed together; hour windows ignore `daily_value`.
2. A provider relayed through Apple Health while also connected directly: the origin is flagged `relayed`, and defaults exclude it.
3. Cross-source sum requires acknowledgement and always carries its warning.
4. An additive day built from hourly picks (`compose: {from: hour}`, E9) carries warning `composite_exceeds_any_source` when it exceeds every single source.
5. Intervals crossing a window boundary are pro-rated.
6. Missing sleep stages give `no_stage_data`, not 0.
7. A night split by a long wake: fragments ≤ 60 min apart merge; otherwise separate episodes.
8. Partial wear below coverage gives `below_quality`, so `first_available` falls through for that window only.
9. Fallback is per window. Series carry a `source` per point; summaries report `sources_used`.
10. Semantically different metrics (SDNN vs RMSSD, provider scores) can never be pooled.
11. Manual entries are flagged and excluded from `heart_rate` by default.
12. Late or corrected data: dirty marks trigger recompute; `computed_at` shows freshness.
13. DST and travel: day windows use stored `local_date`; buckets and hours use the wall clock.
14. Duplicate weigh-in via relay: the `relayed` exclusion applies, else tie by group order.
15. Today: `partial: true`; `max_staleness` lets a stale preferred source fall back.

## Cache and materialization

- `source_hourly_aggregates(user, metric, source_key, hour)` stores, per source and local hour of the owner's timeline, sample count, covered 5-minute buckets, sum of bucket means, min, max, pro-rated interval sum, and first/last timestamps (daily values excluded). The `rebuild_aggregates` job consumes `resolution_dirty` marks older than a minute, rebuilds the marked days and their neighbours (aggregated in SQL, 31 local days per statement, so memory stays flat), and deletes the marks in one transaction per 1,000 marks; requests that met a pending mark queue it, and it also runs daily. The aggregates serve coverage and range views ([J10.5](../plan/E10-query-api/J10.5-coverage-status.md)). Explained results read `measurements`, because they list the rows behind a value.
- `resolved_cache(user, metric, window_kind, local_date, rule_ref, overrides_fp)` holds one local date's results. A date reads only the rows a request for that date alone would load, so requests read their closed dates from the cache and compute only the span of the rest. It is written for dates whose windows have all closed and that have no pending dirty mark on what they read; never for bucket windows, previews with a draft rule, or the all-sources drilldown.
- Invalidation runs in the writer's transaction (triggers of the `resolution_cache` migration). Each row lists what it read in `deps`: the metric, the codes it loads, the wear metric, a follow leader's deps, `sleep` for night windows and sleep contexts, and `workouts` for workout contexts. Its `dep_from..dep_to` dates run from three days before to two after, plus the wear lookback or the latest lookback. A dirty mark deletes the rows that list its code on a date in that range: derived codes, followers, and night D+1 for a session with `sleep_date` D ([ADR-0009](../adr/0009-sleep-date-night-window.md)). Overrides mark dirty too. Rule activation deletes the rows that list the rule's metric. A workout change deletes rows with `workouts` in deps. A timezone period, device type or model, or origin name change clears the owner's rows.
- `vitamux resolve verify [--windows N]` fills the cache over a range, then compares N random (metric, date) pairs from the cache with live single-date runs ([baseline](../benchmarks/baseline.md#resolution-and-cache)). Truncating both tables is always safe.

## Display rollups

`/resolved/summary` and `/resolved/trend` ([api](api.md#owner-endpoints-apiv1)) are display rollups, not a resolution strategy. They read the same daily windows as `/resolved/daily` through the engine and `resolved_cache`, then compute plain statistics of the resolved daily values per period: n (dates with a value), coverage (n / dates), mean, min, max, and sum for additive metrics; a family gets them per code. Windows still open are left out. Nothing is written back, and each response names the rule version it used. `/sources/series` reads `source_hourly_aggregates` per source and resolves nothing.
