# ADR-0008 Resolution rule schema v1

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
The engine (J09.4–J09.10), the rules UI (J11.4) and the built-in defaults ([resolution-defaults.md](../architecture/resolution-defaults.md)) all depend on one rule language. It must stay typed (no expression language) and cover the MVP extensions ([resolution.md › Rule specification](../architecture/resolution.md#rule-specification), [Extensions](../architecture/resolution.md#extensions)).

## Decision
- **Contract:** `schemas/resolution-rule.v1.json`, id `vitamux.rule/1`, stored as JSONB. Frozen at gate G2: within v1 only additive, optional changes (new optional fields, new enum values, new rule families). `internal/resolve.ParseRule` decodes strictly and `Validate` adds the catalogue checks the JSON Schema cannot express (allowed windows and strategies per aggregation, families, wear metric, follow leader). Errors carry RFC 6901 pointers.
- **Fields:** `metric`, `window` (`kind`, `size` for buckets), ordered `groups` of ORed selectors, `exclude`, `within_source` (`intra_group`, `daily_value_policy`, E2 `statistic` + `span`), `strategy` (`op`, `min_sources`, `on_insufficient`), `quality` (coverage, plausible range, flags, staleness, E3 `require_wear`, `sleep`), E1 `contexts`, E5 `follow`, E9 `compose`, `acknowledged_warnings`. Durations are strings with one unit `s|m|h|d` (`30d` is needed by `vo2max`).
- **Membership:** exclusions win over every group; otherwise an input joins the first group (in rule order) with a matching selector. Membership never changes per window: contexts (E1) only reorder priority. `@workout_source` in `contexts.workout` stands for the group of the source that recorded the workout.
- **Rule families:** `metric: sleep` (every sleep-derived code) and `metric: blood_pressure` (bp_reading components) select one event or reading for all their codes; per-code rules for members are rejected. Blood pressure takes selecting strategies only, so one reading never mixes sources.
- **Window:** the rule's `window` is its default window. A request for another kind the catalogue allows reuses groups, strategy and gates; `compose` applies only to `local_day` and a `statistic` only to the kinds it is valid for.
- **Follow (E5):** the follower uses its own group with the id the leader selected, else falls back through its own ladder with `follow_unavailable`. Leader and follower rules use the same window; cycles are rejected (`ValidateSet`).
- **Pooling:** `mean`, `minimum`, `maximum` are rejected when `catalog.Metric.Poolable` is false (provider-scoped scores, selection-only metrics). `sum_across_sources` and `intra_group: sum` need `cross_source_sum_duplicate_risk` acknowledged; the warning still appears in every result.

## Alternatives considered
- **Expression language** (CEL, SQL fragments): flexible, but unexplainable results and an unbounded validator.
- **Context-dependent membership:** lets a context move inputs between groups, but the same row would land in different groups per window, which breaks the all-sources view.
- **One rule per sleep or BP code:** simpler storage, but codes could select different sources and splice one night or reading.

## Consequences
- J09.2 builds defaults and storage on these types and calls `ValidateSet` on activation; J11.4 can reuse the schema for client-side checks.
- Brand groups in defaults expand J15.1's named origin sets into `origin_key` selectors at build time; there is no origin-set selector in v1.
- Workouts have no catalogue metric, so no workout rule exists yet; a `workout` family can be added within v1.
- A v2 needs a migration that rewrites stored specs.
