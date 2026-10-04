# ADR-0021 Source setup in the web panel

Status: Proposed · Date: 2026-10-04 · Deciders: owner

## Context
Every source must be set up from the panel without file edits or restarts, except the one line that turns on an optional sidecar ([E20](../plan/E20-guided-setup/README.md), [connectors#source-setup-in-the-web-panel](../architecture/connectors.md#source-setup-in-the-web-panel)). Secrets follow [ADR-0011](0011-secrets-vault.md).

## Decision
- **Three kinds of value, one home each.**
  - Provider app credentials (an OAuth client id and secret, e.g. Withings): table `provider_app_credentials`, one row per provider; the secret is sealed with purpose `credentials`, AAD `provider_app:<provider>`.
  - Account credentials (tokens): the existing `credentials` table.
  - Sidecar shared secrets: a generated file. For the bundled sidecars the Compose files generate it on every start (a one-shot that needs no Vitamux binary, so a newer Compose file still works with an older image), in one volume per sidecar; for other environment-registered sidecars `vitamux admin init-secrets` writes it. Sidecars added in the panel: table `sidecars`, sealed with AAD `sidecar:<name>`.
- **Precedence.** An environment or `*_FILE` value wins and is reported as `managed_by_environment`; the panel shows it read-only and refuses to change it. Removing it falls back to the stored value.
- **Liveness.** Connectors read app credentials on every authorization, exchange, refresh and verify. Sidecars added or removed in the panel join or leave the registry at once. Nothing needs a restart.
- **Handling.** Secrets are write-only: no endpoint returns one, except the generated secret of a new panel sidecar, once, in the response that creates it. Only an owner session (not an API key) changes them. Every change and every verify is audited (`provider_app.set|delete|verify`, `sidecar.add|remove`) without values. `vitamux keys rotate` re-seals both tables; backups carry them; `purge-user` deletes the rows the owner wrote; exports never contain them.
- **Setup state.** `GET /api/v1/providers` gives each provider one `setup_state`, checked in this order:

  | State | Meaning | Panel action |
  | --- | --- | --- |
  | `needs_sidecar` | A sidecar provider whose sidecar does not answer `describe` (not running, or its secret is missing) | The card with the exact enable line for this install and **Check again** |
  | `connected` | At least one connection that is not disconnected | Manage it under Connections |
  | `needs_public_url` | An OAuth provider whose callback the provider will refuse: `VITAMUX_PUBLIC_URL` is not https, has an IP or localhost host, a port other than 443 or 80, or the callback is over 255 characters (production only; in development the same `problems` are warnings) | Explain the fix; nothing to enter |
  | `needs_app_credentials` | The connector runs on the owner's own provider application and no client id and secret are set | The app wizard (callback URL, then the credentials, then verify) |
  | `ready` | Nothing missing | Connect |

  `managed_by_environment` is a flag on the app credentials, not a state: such a provider is still `ready` or `connected`.
- **No Docker control.** Vitamux never gets the Docker socket, so it cannot start a sidecar. Bundled sidecars (`garmin`, `whoop`) are always registered; while one does not answer, the API returns the enable instruction for the install (`VITAMUX_INSTALL`): `COMPOSE_PROFILES=<name>` then `docker compose up -d` for Compose, `<NAME>_SIDECAR=1` then a redeploy for Coolify, whose profile support is unreliable.
- **No password storage.** Garmin and WHOOP passwords and MFA codes pass through the sidecar once and are never stored ([ADR-0017](0017-sidecar-protocol.md)).
- **Verify.** A connector that can check app credentials without a user grant does so (Withings: a signed `getnonce`); otherwise verify answers `unverifiable` and the first connect checks them.

## Alternatives considered
- **Environment only**: needs file edits and a restart, which the owner ruled out.
- **Database wins over the environment**: an automated install could not pin its values.
- **Reveal endpoint for stored secrets**: no use case that a replace does not cover, and a leak path.
- **Docker socket to start sidecars**: root-equivalent access for a convenience.

## Consequences
- A connector that needs an app implements `connectors.AppConnector`; its secrets never travel through `Descriptor`.
- An unreachable bundled sidecar is a normal state, logged at debug level only.
- The UI flows (the Withings wizard J20.3, the sidecar card J20.4, the sign-in dialog J20.5, first run J20.6) build on these states and problem codes.
