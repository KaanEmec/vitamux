# E25 Catalogue completeness and mapping corrections (v0.3.1)

Release: v0.3.1 (with E24) · Status: done (2026-10-05) · Depends on: E07, E08, E15, E18, E19 · [Plan index](../README.md)
Read first: [metric-catalog#rules](../../architecture/metric-catalog.md#rules), [connectors#normalizer-contract](../../architecture/connectors.md#normalizer-contract), the provider pages ([withings](../../providers/withings.md), [garmin](../../providers/garmin.md), [whoop](../../providers/whoop.md))

**Objective:** Every health value a provider sends is catalogued, stored and shown, and every mapping matches the provider's official or best-verified unofficial documentation.

Owner decision (2026-10-04): as a rule, all data coming from providers is catalogued and brought into the database. Identifiers, baselines, UI text and values derivable from stored rows stay raw, each with a recorded reason.

Mapping research (2026-10-04) checked each provider against its official API reference, open-source clients and fixtures, and [Open Wearables](https://openwearables.io); verified facts are recorded in the provider pages by each job. Bugs it found:
- **Withings:** live `getmeas` names the model `modelid`, not `model_id` as in the spec, so every Withings device is stored without a type and the blood-pressure and scale groups never match.
- **WHOOP:** sleep events use `LIGHT`, `SWS`, `REM`, `WAKE`, `DISTURBANCES`, `LATENCY`; the normalizer knows none of the last four, so sessions have no deep or awake stages. SpO2, skin temperature, day energy, sleep need and consistency stay raw.
- **Garmin:** per-minute SpO2 comes in the sleep payload, not `daily/spo2` (whose spot lists are null), so no SpO2 is stored. Total energy, intensity minutes, floors intervals, hydration and fitness age are not mapped.
- No `total_energy` code; no nightly codes for SpO2, respiration and skin temperature.

## Acceptance
- A field-ledger test fails when any connector fixture has a raw key that is neither mapped nor listed as raw with a reason.
- Withings devices are typed from `modelid`; WHOOP sessions carry deep and awake stages; Garmin stores per-minute SpO2 from sleep; `total_energy` is stored for Garmin, WHOOP and Withings.
- Every bumped normalizer has goldens, and the CHANGELOG says which `vitamux reprocess` to run.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J25.1](J25.1-policy-ledger-seed.md) | Mapping policy, field ledger and catalogue seed | None | None |
| [J25.2](J25.2-withings-measures.md) | Withings: device types and measure types | J25.1 | None |
| [J25.3](J25.3-withings-activity-sleep.md) | Withings activity, intraday and sleep streams | J25.1, J25.2 | None |
| [J25.4](J25.4-whoop.md) | WHOOP mappings | J25.1 | None |
| [J25.5](J25.5-garmin.md) | Garmin mappings and new streams | J25.1 | None |
| [J25.6](J25.6-garmin-reload.md) | Garmin cold-storage reload (opt-in) | J25.5 | None |
| [J25.7](J25.7-apple-health.md) | Apple Health gaps | J25.1 | None |
| [J25.8](J25.8-release.md) | Reprocess notes and v0.3.1 | E24, J25.2–J25.5, J25.7 | None |

J25.2–J25.5 and J25.7 run in parallel after J25.1.

## Out of scope
- New connectors ([E27](../E27-oura/README.md), [E28](../E28-polar/README.md), [E29](../E29-google-health/README.md)); their codes land with them.
- Calculated values (basal = total − active, BMI, sleep debt): never stored.
