# E28 Polar official connector (v0.3.4)

Release: v0.3.4 · Status: todo · Depends on: E20, E25 · [Plan index](../README.md)
Read first: [connectors#adding-a-connector](../../architecture/connectors.md#adding-a-connector), [connectors#oauth-connection-flow](../../architecture/connectors.md#oauth-connection-flow), [E27](../E27-oura/README.md) (same job shape)

**Objective:** Polar data through AccessLink v3 (OAuth2, user registration, pull notifications and transactions), in process, with every value catalogued.

**Outputs:** `internal/connectors/polar`, normalizers with goldens and field ledgers, a setup wizard, `docs/providers/polar.md`.

Data (research 2026-10-04, to be re-verified in J28.1):
- Streams: daily activity (+ step samples), continuous heart rate (5 min), sleep (30-s hypnogram: 0 awake, 1 rem, 2 and 3 light, 4 deep, 5 unknown; no in-bed time, so latency stays null), nightly recharge (HRV and breathing samples), SpO2 tests (passed tests only), skin temperature, cardio load, exercises.
- Codes: `steps`, `active_energy` (excludes BMR), `total_energy`, `distance_walk_run`, `heart_rate`, `hrv_rmssd`, `hrv_rmssd_nightly` (window in context), `respiratory_rate`, `respiratory_rate_nightly`, `spo2`, `skin_temperature_nightly`, `sleep_temperature_deviation`, and the scores `polar_nightly_recharge`, `polar_ans_charge`, `polar_sleep_score`, `polar_cardio_load`, `polar_cardio_strain`, `polar_cardio_tolerance`, `polar_active_time`, `polar_daily_activity`.

## Acceptance
- The fake-Polar lifecycle passes in CI (register, connect, transaction pull and commit, refresh or re-consent, deregistration); the setup wizard verifies app credentials.
- Every raw key in the fixtures is in a ledger; Polar sources take part in the built-ins (watch groups) and the default rule.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J28.1](J28.1-verify-api.md) | API verification and synthetic fixtures | None | None |
| [J28.2](J28.2-oauth-setup.md) | OAuth, user registration and setup wizard | J28.1 | None |
| [J28.3](J28.3-streams.md) | Transaction streams | J28.2 | None |
| [J28.4](J28.4-normalizers.md) | Normalizers and catalogue codes | J28.1, J25.1 | None |
| [J28.5](J28.5-lifecycle-release.md) | Lifecycle test, docs and v0.3.4 | J28.3, J28.4 | None |
