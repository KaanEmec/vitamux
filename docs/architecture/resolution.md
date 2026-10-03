# Resolution

Resolution turns many source rows into one explained value per (metric, window). It only reads canonical rows ([data-model.md](data-model.md)) and only writes rebuildable cache.

## Rule specification

A rule is stored as JSONB in `resolution_rules.spec` and validated by `schemas/resolution-rule.v1.json`. There is no expression language.

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

- Selector fields: `provider`, `connection_id`, `origin_key`, `origin_key_prefix`, `origin_name`, `relayed`, `device_type`, `device_model`, `device_id`, `entry` (`device|manual`). Fields within one selector are ANDed; a list of selectors is ORed.
- Validation rejects:
  - unknown fields;
  - windows or strategies not allowed for the metric's aggregation;
  - `single_source` with more than one group; empty groups;
  - `sum_across_sources` or `intra_group: sum` without `acknowledged_warnings: [cross_source_sum_duplicate_risk]`.
- Every edit creates an immutable version, and `active_rules` points to one. Built-in defaults live in code as `builtin:<metric>:<n>` and are copied on first edit.

## Windows

| Kind | Definition (in the user's timezone for that date) |
| --- | --- |
| `bucket(size)` | 1, 5, 15, or 30 min, aligned to local midnight. DST follows the wall clock. |
| `hour` | Local hours. DST days have 23 or 25. |
| `local_day` | Rows with stored `local_date = D`, so a travel day is not split |
| `local_night` | Main sleep episode with `sleep_date = D`; candidates end in `[D−1 anchor, D anchor)` |
| `sleep_episode` | Every aligned episode, naps included |
| `latest` | Most recent valid input at or before `as_of` |
| `reading` | One result per measurement group (e.g., each BP reading) |

A window ending in the future returns `partial: true`.

## Within-source aggregation

This happens per source group, before any cross-source step, so dense sources do not dominate.

| Aggregation | Within-source value |
| --- | --- |
| intensive | Samples go into base buckets (`min(window, catalogue base_bucket)`). Bucket mean per group; several sub-sources in a group use the `intra_group` mean of their bucket means. Window value = mean of covered bucket means, **each bucket weighted equally**. Coverage = covered buckets / elapsed buckets. |
| additive | Intervals pro-rated linearly by overlap (flag `prorated`). For `local_day` with `prefer_reported`, use the provider `daily_value` if present, else the interval sum — **never both**. Sub-sources in a group use the per-bucket **max**, so iPhone + Watch steps are not added. |
| latest | Latest valid reading. Group metrics select whole groups. |
| daily_summary | The source's `daily_value` for D, else the latest sample in D |
| sleep_derived | Sum over that group's sessions in the aligned main episode. Missing stages → `no_stage_data`, **not 0**. |

Example for one 5-min bucket: source A has 50 samples averaging 62.1, source B has 1 sample of 66. The mean across sources is (62.1 + 66) / 2 = **64.05**. A pooled-sample mean would be 62.18.

## Strategies

For each window, independently:

1. Assign inputs to groups. Excluded inputs are reported as such.
2. Compute each group's value and apply the quality gates: plausible range, flags, coverage, staleness, episode coverage, overrides. Group status becomes `valid | no_data | below_quality | excluded | stale | not_aligned | no_stage_data`.
3. Apply the strategy:

| Op | Result | When inputs are insufficient |
| --- | --- | --- |
| `single_source` | The single group's value | `no_data` with reason |
| `first_available` | First valid group in order | Implicit fallback; skipped groups carry reasons |
| `mean_/minimum_/maximum_across_sources` | Over valid groups | Below `min_sources`: `use_available` (warning `insufficient_sources`) or `no_value` |
| `sum_across_sources` | Sum of valid groups | Only if acknowledged; the warning is repeated in every result |
| `latest` / `earliest` | Across groups; ties go by group order | — |
| `event_priority` | Whole event (session with stages, workout) from the first group with an aligned event | Stages are never spliced across sources |

4. Apply overrides.
5. Emit the result.

## Sleep episode alignment

1. Gather candidate sessions for the night. Merge same-source fragments that are ≤ 60 min apart.
2. Link sessions across sources when `overlap / min(duration_a, duration_b) ≥ match_overlap`. Connected components are episodes.
3. The main episode is the one with the largest union span. Others are secondary (naps unless `include_naps`).
4. A group whose sessions cover less than `min_episode_coverage` of the main episode is `below_quality: partial_episode`.

Example: A 23:10–06:55 and B 23:40–07:05 overlap by 0.98, so they are matched and deep sleep is averaged. If A had only 03:00–07:00, its coverage would be 0.51 < 0.7, so A is excluded and B is used with warning `insufficient_sources`.

Workouts cluster the same way: overlap ≥ 0.6 of the shorter and a compatible sport. Each cluster is listed once, with alternates.

## Group-coherent selection

BP and body-composition components always come from the same reading. `blood_pressure` returns systolic, diastolic, and pulse together. A daily mean averages each component across selected readings and never mixes sources within one reading.

## Manual overrides

`manual_overrides` are scoped to (metric, window kind, window key) and support three actions:

- `exclude_input`: a row, session, group, or whole source group for that window;
- `force_source(group)`;
- `set_value(value, unit, note)`: the result status becomes `overridden`, and the computed result is kept.

Overrides never touch source rows. They are revocable and audited. Fixing a bad measurement means `exclude_input` or a `manual` provider entry.

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

`status` ∈ `direct | fallback | calculated | overridden | no_data`. Explanations come from fixed templates.

## Edge cases

Each case becomes a scenario test in J09.8.

1. A daily total and intraday intervals from one source are never summed together; hour windows ignore `daily_value`.
2. A provider relayed through Apple Health while also connected directly: the origin is flagged `relayed`, and defaults exclude it.
3. Cross-source sum requires acknowledgement and always carries its warning.
4. An additive day built from hourly maxima (`derive_from: hour`) carries warning `composite_exceeds_any_source`.
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

- `source_hourly_aggregates(user, metric, source_key, hour)` stores sample count, covered buckets, sum of bucket means, min, max, pro-rated interval sum, and first/last timestamps. It is rebuilt for dirty days by the `rebuild_aggregates` job and serves day and range windows. Buckets of 5 min and finer read `measurements` directly.
- `resolved_cache(user, metric, window_kind, window_key, rule_ref)` is written on miss. It is deleted in the writer's transaction on a dirty mark, override, or rule activation.
- `vitamux resolve verify --sample N` compares cache with live computation in CI. Truncating both tables is always safe.
