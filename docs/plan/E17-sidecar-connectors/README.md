# E17 Remote sidecar connectors and third-party collectors (v0.2.0)

Release: v0.2 · Depends on: E06, E07, E11, G4 · [Plan index](../README.md)
Read first: [connectors#remote-sidecar-mode](../../architecture/connectors.md#remote-sidecar-mode), [connectors#third-party-collectors](../../architecture/connectors.md#third-party-collectors), [security#threat-model](../../architecture/security.md#threat-model)

**Objective:** Let the community plug any source into Vitamux, including existing open-source collectors in other languages, without changing the core. Vitamux schedules, stores raw data and normalizes. A sidecar only authenticates and fetches.

**Outputs:** Frozen protocol `vitamux-connector/1`, the `remote` execution mode in the core, a conformance kit, a non-Go example sidecar, packaging and licensing conventions for third-party collectors, and automatic tracking of wrapped upstream releases.

Numbered after E16, but it is a product epic that comes **before** E16 in dependency order. The unofficial Garmin ([E18](../E18-garmin/README.md)) and WHOOP ([E19](../E19-whoop/README.md)) connectors build on it.

## Acceptance
- A sidecar registered only through configuration (URL plus a shared-secret file) appears in the Connections UI. It completes its auth flow in the existing wizard, including MFA challenges, and syncs, backfills and reports health like an in-process connector.
- The example sidecar passes `vitamux connector-test` in CI. A broken sidecar fails with a clear message for each check.
- A sidecar crash, timeout or protocol violation changes only that connection's state. The core and other connections are unaffected.
- The guide alone is enough to wrap an upstream open-source collector, either as a sidecar or as a push collector.
- A green upstream release reaches the sidecar's `stable` image without human action; a breaking one never does.

## Parallelism
- J17.1 can start once E06 and E07 are done. J17.2 needs J11.2 for the UI part.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J17.1](J17.1-protocol.md) | Protocol `vitamux-connector/1` contract | J06.4 | None |
| [J17.2](J17.2-remote-runtime.md) | Remote execution mode in the core | J17.1, J06.5, J11.2 | None |
| [J17.3](J17.3-conformance-kit.md) | Conformance kit and example sidecar | J17.1, J17.2 | None |
| [J17.4](J17.4-third-party-packaging.md) | Third-party collector packaging, licensing and guide | J17.3, J14.2 | None |
| [J17.5](J17.5-upstream-tracking.md) | Upstream tracking: automatic bumps, canary builds, sidecar image channel | J17.3, J17.4, J13.6 | None |
