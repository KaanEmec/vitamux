# E27 Oura official connector (v0.3.3)

Release: v0.3.3 · Status: todo · Depends on: E20, E25 · [Plan index](../README.md)
Read first: [connectors#adding-a-connector](../../architecture/connectors.md#adding-a-connector), [connectors#oauth-connection-flow](../../architecture/connectors.md#oauth-connection-flow), [connectors#source-setup-in-the-web-panel](../../architecture/connectors.md#source-setup-in-the-web-panel)

**Objective:** Oura data through the official API v2 (OAuth2), in process, following the Withings pattern, with every value catalogued.

**Outputs:** `internal/connectors/oura`, normalizers with goldens and field ledgers, a setup wizard, `docs/providers/oura.md`.

Data (research 2026-10-04, to be re-verified in J27.1):
- Streams: daily activity, heart rate (5-min), sleep (30-s phases: 1 deep, 2 light, 3 rem, 4 awake; `rest` and `deleted` sessions excluded; `long_sleep` is the main-sleep candidate), daily sleep, daily readiness, daily SpO2, daily stress, daily resilience, VO2max, cardiovascular age, workouts.
- Codes: `steps`, `active_energy` (≥ 1.5 MET only), `total_energy`, intensity times, `heart_rate`, `hrv_rmssd`, `hrv_rmssd_nightly`, `respiratory_rate_nightly`, `spo2_nightly`, `sleep_temperature_deviation`, `resting_heart_rate` (context: lowest 30-s value), and the scores `oura_readiness`, `oura_sleep_score`, `oura_activity_score`, `oura_resilience`, `oura_breathing_disturbance_index`, `oura_restless_periods`, `oura_temperature_trend_deviation`, `oura_cardiovascular_age`, `oura_equivalent_walking_distance` (never `distance_walk_run`).

## Acceptance
- The fake-Oura lifecycle passes in CI (connect, backfill, incremental, refresh, revoked → reauth); the setup wizard verifies app credentials.
- Every raw key in the fixtures is in a ledger; Oura sources take part in the built-ins (ring groups) and the default rule.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J27.1](J27.1-verify-api.md) | API verification and synthetic fixtures | None | None |
| [J27.2](J27.2-oauth-setup.md) | OAuth connection and setup wizard | J27.1 | None |
| [J27.3](J27.3-streams.md) | Streams | J27.2 | None |
| [J27.4](J27.4-normalizers.md) | Normalizers and catalogue codes | J27.1, J25.1 | None |
| [J27.5](J27.5-lifecycle-release.md) | Lifecycle test, docs and v0.3.3 | J27.3, J27.4 | None |
