# E18 Garmin Connect connector (unofficial sidecar, v0.3.0)

Release: v0.3 · Depends on: E17, G5 for release · [Plan index](../README.md)
Read first: [connectors#third-party-collectors](../../architecture/connectors.md#third-party-collectors), [connectors#remote-sidecar-mode](../../architecture/connectors.md#remote-sidecar-mode), [J17.5](../E17-sidecar-connectors/J17.5-upstream-tracking.md)

**Objective:** Anyone with a Garmin Connect account, and no Garmin developer program access, can sync their Garmin data into Vitamux with granular detail.

**Upstream (owner decision, 2026-10-04):** [`cyberjunky/python-garminconnect`](https://github.com/cyberjunky/python-garminconnect) (PyPI `garminconnect`, MIT). It is the most active option: releases every one to two weeks, 150+ endpoints, and since 0.3.0 its own login over Garmin's mobile SSO flow with MFA. `garth`, which older versions used, is deprecated upstream. The official Garmin Health API needs a developer agreement, which this epic exists to avoid. J18.1 confirms the choice; the sidecar follows upstream releases through [J17.5](../E17-sidecar-connectors/J17.5-upstream-tracking.md).

**Outputs:** `sidecars/garmin/` (Python, wraps the library, speaks `vitamux-connector/1`), provider `garmin` (`Official=false`), Go `garmin.*` normalizers, catalogue codes, `docs/providers/garmin.md`, a Compose profile.

The provider code is `garmin`, so the existing built-in groups `garmin` and `garmin_apple` ([resolution-defaults](../../resolution-defaults.md)) fill without rule changes.

## Acceptance
- A user signs in with email, password and an MFA code in the panel ([J20.5](../E20-guided-setup/J20.5-signin-wizard.md)). Vitamux stores only the resulting tokens (sealed); the password is never stored or logged.
- Backfill and incremental sync bring in intraday HR, steps, stress and Body Battery, sleep with stages, HRV, SpO2, respiration, activities (with original FIT files kept raw), body composition, blood pressure and training metrics, with no gaps or duplicates.
- Garmin data relayed through Apple Health and Garmin data fetched directly stay distinguishable, and the built-in rules prefer the direct path.
- A Garmin login or response-shape change sets `needs_reauth` or `degraded: schema_drift` on that connection only; no other data is substituted.
- An upstream release reaches the `stable` sidecar image automatically when all checks pass ([J17.5](../E17-sidecar-connectors/J17.5-upstream-tracking.md)).

## Parallelism
- J18.1 can start now; it needs no code. J18.2 needs J17.2 and J17.3. J18.4 can run alongside J18.2 and J18.3 on J18.1's synthetic fixtures.
- E18 and [E19](../E19-whoop/README.md) run in parallel.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J18.1](J18.1-verify-upstream.md) | Upstream and API verification, ADR, fixtures | J04.1 | None |
| [J18.2](J18.2-sidecar-auth.md) | Garmin sidecar and sign-in | J18.1, J17.2, J17.3 | None |
| [J18.3](J18.3-streams.md) | Streams, planning and backfill | J18.2, J06.6 | None |
| [J18.4](J18.4-normalizers.md) | Go normalizers and catalogue codes | J18.1, J07.4 | None |
| [J18.5](J18.5-lifecycle-tracking.md) | Lifecycle tests and upstream tracking | J18.3, J18.4, J17.5 | None |
| [J18.6](J18.6-docs-install.md) | Install, docs and real-account check | J18.5, J17.4 | None |
