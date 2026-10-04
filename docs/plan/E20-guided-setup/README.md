# E20 Guided source setup in the web panel (v0.2.1)

Release: v0.2.1 (owner decision, 2026-10-04) · Depends on: E11, E17; J20.4 and J20.5 need E18 and E19 · [Plan index](../README.md)
Read first: [ADR-0021](../../adr/0021-source-setup.md), [connectors#source-setup-in-the-web-panel](../../architecture/connectors.md#source-setup-in-the-web-panel), [frontend#navigation](../../architecture/frontend.md#navigation), [security#keys-and-secrets](../../architecture/security.md#keys-and-secrets)

**Objective:** Every source is set up from the Vitamux web panel in a few guided steps. This covers keys, secrets, OAuth app credentials, sign-in and MFA. No `.env` edit, secret file or restart is needed, except the one-time line that turns on an optional sidecar container.

Owner decision (2026-10-04): Withings, Garmin and WHOOP must have a very easy setup, with every secret, key and OAuth value entered through the web panel.

**Outputs:** sealed provider app credentials in PostgreSQL with an API; a per-provider setup state; the Withings setup wizard; sidecars wired automatically with shared secrets; a sign-in and MFA wizard for Garmin and WHOOP; a first-run "Connect a source" flow; install docs where the UI is the main path.

## Acceptance
- **Withings:** on a fresh install, the owner goes from "no app" to a connected account inside the panel. The panel shows the callback URL to register at Withings, takes the client id and secret, checks them, and starts OAuth. No restart is needed.
- **Garmin and WHOOP:** with the sidecar profile on (one line in `.env`, or a toggle in Coolify), the owner connects an account with email, password and an MFA code in one dialog. Without the profile, the panel shows exactly what to change and detects the sidecar once it runs.
- **Secrets:** entered secrets are write-only. They are sealed with the master key, covered by key rotation, backup and restore, and the redaction audit, and changing one is audited. A value set through the environment still wins and shows as "managed by the environment".
- **Errors:** each failure (wrong secret, callback mismatch, http public URL, wrong MFA code, rate limit, sidecar not running) tells the owner what to do next in plain language.

## Parallelism
- J20.1 can start now. J20.2 and J20.3 (Withings) need only shipped code, so they can land in v0.2.0.
- J20.4 needs J17.2. J20.5 needs J18.2 and J19.2.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J20.1](J20.1-setup-adr.md) | Setup model ADR and UX flows | None | None |
| [J20.2](J20.2-app-credentials.md) | Provider app-credential store and setup API | J20.1, J03.1, J10.4 | None |
| [J20.3](J20.3-withings-wizard.md) | Withings setup wizard | J20.2, J11.2 | None |
| [J20.4](J20.4-sidecar-wiring.md) | Sidecars wired automatically and detected in the panel | J20.1, J17.2 | None |
| [J20.5](J20.5-signin-wizard.md) | Sign-in and MFA wizard for Garmin and WHOOP | J20.4, J18.2, J19.2 | None |
| [J20.6](J20.6-first-run-e2e.md) | First-run flow, end-to-end tests and install docs | J20.3, J20.5 | None |

All jobs done 2026-10-04; E20 ships as v0.2.1.

## Follow-ups
- Owner acceptance of [ADR-0021](../../adr/0021-source-setup.md) (still Proposed).
- A wrong MFA code ends the flow: the core consumes the auth state on every continue, so the owner signs in again. Keep the state for a refused code so a retry needs only the code (the Garmin sidecar already keeps its pending sign-in).
- The unofficial-source acknowledgement still means "starts paused": `auth/begin` has no field to start the connection active once acknowledged.
- MFA method choice and resend (WHOOP app or SMS): the sidecar protocol has no step for them.
- Plain-language errors for a locked account or a captcha, and for an expired MFA session as distinct from a wrong code: sidecars report both as `reauth_required`.
- The real-stack smoke test (T20.6.3) with replay sidecars, a fresh-user run (T20.6.4), and a live Coolify check of the sidecar toggle (T20.4.2).
