# E15 Apple Health Bridge (required, v0.2.0)

Release: v0.2 · Depends on: E05, E07, E09, E11, G3 · [Plan index](../README.md)
Read first: [apple-health#structure](../../architecture/apple-health.md#structure), [apple-health#sync-algorithm](../../architecture/apple-health.md#sync-algorithm), [apple-health#origins-and-relays](../../architecture/apple-health.md#origins-and-relays)

**Objective:** Automatic, incremental, origin-preserving Apple Health sync through a reusable Swift package and minimal SwiftUI app, plus a manual export importer.

**Outputs:** HealthBridgeKit, HealthBridgeApp, pairing endpoints, `healthkit.samples` normalizer, device UI, export importer, device test report, v0.2.0.

## Acceptance
- On a physical iPhone, selected types sync incrementally with deletions honoured.
- A rule can prefer Apple Watch HR and exclude relayed samples.
- Background behaviour documented from real observations.
- Export importer works on a synthetic export.
- Gate G5 passed.

## Parallelism
- J15.1, J15.3, J15.4 may start after G3, in parallel with the MVP; completion follows G4.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J15.1](J15.1-platform-contract.md) | Platform verification and contract (ADR-014) | J05.1 | None |
| [J15.2](J15.2-backend-pairing-normalizer.md) | Backend pairing and HealthKit normalizer | J15.1, J05.4, J07.4 | None |
| [J15.3](J15.3-kit-core.md) | HealthBridgeKit Core | J15.1 | None |
| [J15.4](J15.4-kit-healthkit.md) | HealthBridgeKit HealthKit module | J15.1, J15.3 | None |
| [J15.5](J15.5-app.md) | HealthBridgeApp (SwiftUI) | J15.4 | None |
| [J15.6](J15.6-configurator-devices.md) | Configurator device and origin integration | J15.2, J11.4, J11.5 | None |
| [J15.7](J15.7-device-campaign.md) | Physical-device test campaign | J15.5, J15.2 | None |
| [J15.8](J15.8-export-importer.md) | Apple Health export importer | J15.2, J13.2 | None |
| [J15.9](J15.9-release.md) | Release docs and v0.2.0 | J15.6, J15.7, J15.8, J14.5 | G5 |
