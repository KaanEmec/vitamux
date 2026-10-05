# E26 Intraday views on the server and the panel (v0.3.1)

Release: v0.3.1 (with E24 and E25) · Status: done (2026-10-05) · Depends on: E23, E25 · [Plan index](../README.md)
Read first: [J22.26](../E22-ios-app/J22.26-intraday-views.md) (the resolution ladder), [resolution#windows](../../architecture/resolution.md#windows), [frontend#chart-grammar](../../architecture/frontend.md#chart-grammar)

**Objective:** A metric can be looked at inside a day at the resolution that suits it: heart rate down to 30-second buckets and raw readings, steps as 30-minute bars, nothing finer than a day for metrics measured once. This epic delivers the catalogue, engine, API and panel half of J22.26; the iOS app's Day view stays in J22.26 and builds on it.

Ladder adjustments from the 2026-10-04 provider research (native sample spacing):
- Heart rate: WHOOP 6 s, Google/Fitbit 1 s, Apple workouts seconds, Garmin about 2 min, Oura and Polar 5 min. The 1 min → 30 s → raw ladder stays.
- SpO2, respiratory rate, skin temperature, Garmin stress and body battery arrive every 1–5 min, so they move to the sparse class (5 min → raw).
- Additive metrics keep 30 min → 1 min, but a source is never drawn finer than it was sent (Garmin steps are 15-min intervals).
- `*_nightly` codes have no intraday view; they open the night view.

## Acceptance
- Every metric with `intraday` opens a Day view in the panel; metrics without it show no Day range.
- Zooming heart rate from 24 h to 15 min passes through 1 min, 30 s and raw, with the min–max band, source series, night and workout overlays.
- Steps show 30-minute bars that sum to the day's resolved total within pro-rating.
- A WHOOP day of 6-second heart rate plus a second source resolves to 1-minute buckets in under 300 ms on the synthetic dataset; raw spans stay under 2,000 points per source per request.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J26.1](J26.1-intraday-catalogue.md) | Intraday metadata in the catalogue | None | None |
| [J26.2](J26.2-fine-buckets-api.md) | 30-second buckets and fine series grains | J26.1 | None |
| [J26.3](J26.3-panel-day-view.md) | Panel Day view | J26.2 | None |
| [J26.4](J26.4-performance-release.md) | Performance, docs and v0.3.1 | J26.3 | None |
