# E29 Fitbit and Pixel through the Google Health API (blocked)

Release: when unblocked · Status: blocked (J29.1) · Depends on: E20, E25 · [Plan index](../README.md)
Read first: [connectors#adding-a-connector](../../architecture/connectors.md#adding-a-connector), [E27](../E27-oura/README.md) (same job shape)

**Objective:** Fitbit and Pixel Watch data through the Google Health API, with every value catalogued.

Owner decision (2026-10-04): the Fitbit Web API stops working on 2026-10-30, and its replacement, the Google Health API, is not onboarding new projects. The epic targets the Google Health API only, with no legacy Fitbit import, and waits until Google accepts new projects.

Data (research 2026-10-04, to be re-verified in J29.1):
- Heart rate stored at 1 s; steps, distance, floors, altitude, active and total energy at 1 min; SpO2 at 1 min during sleep; HRV (RMSSD, SDNN) at 5 min during sleep; daily resting heart rate (method in context), daily HRV, SpO2, respiratory rate and sleep temperature; VO2max; active zone minutes; sleep with stages (AWAKE, LIGHT, DEEP, REM, ASLEEP, RESTLESS, UNSPECIFIED), short awakenings as an overlay, and out-of-bed segments.
- Codes: the regular codes plus `total_energy`, `spo2_nightly`, `respiratory_rate_nightly`, `skin_temperature_nightly`, `fitbit_cardio_fitness`, `fitbit_active_zone_minutes`.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J29.1](J29.1-access-verify.md) | Project access and API verification | None | None |
| J29.2 | OAuth (Google) and setup wizard | J29.1 | None |
| J29.3 | Data-point streams | J29.2 | None |
| J29.4 | Normalizers and catalogue codes | J29.1, J25.1 | None |
| J29.5 | Lifecycle test, docs and release | J29.3, J29.4 | None |

J29.2–J29.5 follow the E27 job files and are written out when J29.1 is done.
