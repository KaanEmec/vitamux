# E24 Resolution defaults, opt-in gates and visible data (v0.3.1)

Release: v0.3.1 (with E25) · Status: done (pending release tag) · Depends on: E09, E21, E23 · [Plan index](../README.md)
Read first: [resolution#extensions](../../architecture/resolution.md#extensions), [resolution-defaults#suggested-defaults](../../architecture/resolution-defaults.md#suggested-defaults), [connectors#remote-sidecar-mode](../../architecture/connectors.md#remote-sidecar-mode)

**Objective:** Every metric a source sends is visible and resolves to a real value. No metric is left without a rule, no gate silently discards a source, and no device that never reports a metric stands in for it with a 0.

Found on a three-source installation (Withings, Garmin, WHOOP):
- Metrics without a built-in (basal energy, floors, every `garmin_*` and `whoop_*` score) resolve to `no_data`, and the metric page hides the all-sources overlay when the resolved series is empty.
- The wear gate treats a worn device that never reports a metric as a measured 0: a WHOOP band (heart rate all day, no active energy, steps only as a daily total) won active energy and steps with 0 on most days without the watch.
- Hour composition drops daily values, so a daily-only source can only ever contribute zeros.
- `active_energy`'s 80 % coverage gate rejected the watch's daily total on partly worn days.
- A retired sidecar stream kept its connection `degraded` for good.

Owner decisions (2026-10-04):
- A metric without a rule uses a **default rule**: the owner's global source order when set, else a generic device ladder.
- Coverage and wear gates are **opt-in**: no built-in sets them; the owner adds them per rule. The Explore source strip starts hidden.
- The built-in heart-rate ladder ranks **WHOOP above Garmin**.

## Acceptance
- Every catalogue code resolves through an owner rule, a built-in or the default rule; `noRule` is gone from the API.
- On the synthetic dataset: a worn band without the metric never yields a value; a daily-only band ranked above the phone gives its daily total; a half-day watch plus a daily-only band never adds the two.
- No built-in has `min_coverage`, `require_wear` or `min_episode_coverage`; sleep no longer applies a 0.7 episode coverage unless the rule sets it.
- Explore draws the source series whenever any source has data, and the dashboard offers `total_energy`.
- A stream the connector stops declaring is retired at the next describe refresh, schedules for newly declared streams appear without re-auth, and a stream the owner disabled never keeps the connection degraded.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J24.1](J24.1-default-rule.md) | Default rule and source priority | None | None |
| [J24.2](J24.2-opt-in-gates.md) | Opt-in quality gates and source strip | None | None |
| [J24.3](J24.3-no-phantom-values.md) | No phantom values: capability and daily totals in hours | None | None |
| [J24.4](J24.4-heart-rate-ladder.md) | Heart-rate ladder: WHOOP above Garmin | J24.2 | None |
| [J24.5](J24.5-explore-dashboard.md) | Explore and dashboard show every source | J24.1, J25.1 | None |
| [J24.6](J24.6-stream-reconcile.md) | Stream reconciliation and the owner's timezone for sidecars | None | None |

J24.1–J24.3 and J24.6 run in parallel. Built-in version bumps from J24.2 and J24.4 land together so each metric gets one new version.

## Out of scope
- New metrics and mapping fixes ([E25](../E25-catalogue-mappings/README.md)).
- Intraday views ([E26](../E26-intraday-views/README.md)).
