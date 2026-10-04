# E19 WHOOP connector (unofficial sidecar, v0.3.0)

Release: v0.3 · Depends on: E17, G5 for release · [Plan index](../README.md)
Read first: [connectors#third-party-collectors](../../architecture/connectors.md#third-party-collectors), [connectors#remote-sidecar-mode](../../architecture/connectors.md#remote-sidecar-mode), [J17.5](../E17-sidecar-connectors/J17.5-upstream-tracking.md)

**Objective:** A WHOOP member syncs WHOOP data into Vitamux at a finer grain than the official API offers, including 6-second heart rate.

**Upstream (owner decision, 2026-10-04):** [`@dofek/whoop`](https://github.com/Asherlc/dofek/tree/main/packages/whoop-whoop), an unofficial TypeScript client for WHOOP's private API (npm, MIT, Node ≥ 22.14). It is published from the `Asherlc/dofek` monorepo in lockstep with its sibling packages (`@dofek/provider-http`, `@dofek/training`), often several times a day. The sidecar follows its releases through [J17.5](../E17-sidecar-connectors/J17.5-upstream-tracking.md). The official WHOOP API offers summaries only; an official OAuth connector could exist beside this one later, under a different provider code.

**Outputs:** `sidecars/whoop/` (Node, wraps `@dofek/whoop`, speaks `vitamux-connector/1`), provider `whoop` (`Official=false`), Go `whoop.*` normalizers, catalogue codes, `docs/providers/whoop.md`, a Compose profile.

The provider code is `whoop`, so the existing built-in `whoop` group ([resolution-defaults](../../resolution-defaults.md)) fills without rule changes.

## Acceptance
- A user signs in with email, password and an MFA code (authenticator app or SMS) in the panel ([J20.5](../E20-guided-setup/J20.5-signin-wizard.md)). Vitamux stores only the resulting tokens (sealed); the password is never stored or logged.
- Backfill and incremental sync bring in 6-second HR, steps, cycles with strain and recovery, sleep with stages, and workouts (including weightlifting detail), with no gaps or duplicates.
- Endpoints the upstream types as `unknown` (strain deep dive, journal) are kept raw and are not normalized until their shape is verified. The journal stream is off by default.
- A rule can prefer WHOOP heart rate over another wearable's, for example during workouts.
- A WHOOP auth or response-shape change sets `needs_reauth` or `degraded: schema_drift` on that connection only; no other data is substituted.
- An upstream release reaches the `stable` sidecar image automatically when all checks pass.

## Parallelism
- J19.1 can start now; it needs no code. J19.2 needs J17.2 and J17.3. J19.4 can run alongside J19.2 and J19.3 on J19.1's synthetic fixtures.
- [E18](../E18-garmin/README.md) and E19 run in parallel. J19.7 releases both.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J19.1](J19.1-verify-upstream.md) | Upstream and API verification, ADR, fixtures | J04.1 | None |
| [J19.2](J19.2-sidecar-auth.md) | WHOOP sidecar and sign-in | J19.1, J17.2, J17.3 | None |
| [J19.3](J19.3-streams.md) | Streams, planning and backfill | J19.2, J06.6 | None |
| [J19.4](J19.4-normalizers.md) | Go normalizers and catalogue codes | J19.1, J07.4 | None |
| [J19.5](J19.5-lifecycle-tracking.md) | Lifecycle tests and upstream tracking | J19.3, J19.4, J17.5 | None |
| [J19.6](J19.6-docs-install.md) | Install, docs and real-account check | J19.5, J17.4 | None |
| [J19.7](J19.7-release.md) | Release v0.3.0 (E18, E19 and E20) | J18.6, J19.6, J20.6, G5 | Produces G6 |
