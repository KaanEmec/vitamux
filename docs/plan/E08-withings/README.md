# E08 Withings official connector

Release: MVP · Depends on: E03, E06, E07 · [Plan index](../README.md)
Read first: [connectors#withings-connector-reference-pattern](../../architecture/connectors.md#withings-connector-reference-pattern), [connectors#adding-a-connector](../../architecture/connectors.md#adding-a-connector)

**Objective:** Prove the connector lifecycle with an official OAuth API: BP, weight and body composition by polling, with optional notifications.

**Outputs:** `internal/connectors/withings`, normalizer, OAuth and webhook routes, `docs/providers/withings.md`.

## Acceptance
- The fake-Withings lifecycle passes in CI: connect, backfill, incremental, notify, refresh rotation, revoked token → reauth.
- A manual test with the owner's real account imports BP readings with paired components.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J08.1](J08.1-verify-api.md) | API verification and fixtures | J04.1 | None |
| [J08.2](J08.2-oauth.md) | OAuth connection flow | J08.1, J06.4, J03.2 | None |
| [J08.3](J08.3-measures.md) | Measures stream and normalizer | J08.2, J06.6, J07.4 | None |
| [J08.4](J08.4-notifications.md) | Notifications (optional per install) | J08.3 | None |
| [J08.5](J08.5-lifecycle-e2e.md) | Lifecycle end-to-end test | J08.2, J08.3, J08.4 | None |
| [J08.6](J08.6-activity-sleep.md) | Activity and sleep streams (post-MVP) | J08.5 | None |
