# E20 Guided source setup in the web panel (v0.3.0)

Release: v0.3 (the Withings jobs may ship earlier) · Depends on: E11, E17; J20.4 and J20.5 need E18 and E19 · [Plan index](../README.md)
Read first: [connectors#source-setup-in-the-web-panel](../../architecture/connectors.md#source-setup-in-the-web-panel), [frontend#navigation](../../architecture/frontend.md#navigation), [security#keys-and-secrets](../../architecture/security.md#keys-and-secrets)

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
