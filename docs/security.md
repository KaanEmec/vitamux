# Security model

What Vitamux protects, from whom, and what it does not. The design details (parameters, keys, roles) are in [architecture/security.md](architecture/security.md); this page is the overview that reviews and PRs check against. Report vulnerabilities as described in [SECURITY.md](../SECURITY.md).

## Assets

| Asset | Where it lives |
| --- | --- |
| Health data: raw provider payloads, normalized records, lab documents and extracted values | PostgreSQL, blob volume (documents encrypted) |
| Provider credentials: OAuth access and refresh tokens | PostgreSQL, AES-256-GCM under the master key |
| Owner credentials: password hash, TOTP secret, recovery codes, sessions | PostgreSQL (argon2id, sealed secret, SHA-256 hashes) |
| Machine tokens: API keys, client and device tokens, webhook tokens | PostgreSQL (SHA-256 only; shown once) |
| Master key | `VITAMUX_MASTER_KEY_FILE`, outside the database and backups |
| Backups and exports | Operator storage; export zips behind admin access and one-time links |

## Actors

| Actor | Trust |
| --- | --- |
| Owner in a browser | Full access after password (+ TOTP) sign-in |
| Scripts with an API key | Only the key's scopes (`read:health`, `read:config`, `write:config`, `write:documents`, `admin`) |
| Clients and devices (Apple Health bridge, importers) | Push raw data into one connection; cannot read |
| Providers (Withings, …) | Send webhooks that are only hints to fetch; their API responses are untrusted input |
| AI extraction providers | Receive a PDF only when the owner enabled that provider and asked for it |
| Anyone on the internet | Reaches only login, OAuth callbacks, webhooks, health probes and the static UI |
| Host administrator | Trusted; a compromised host is out of scope |

## Trust boundaries

1. **Internet → TLS proxy → `vitamux:8080`.** Only the UI, `/api`, `/oauth`, `/webhooks`, `/healthz` and `/readyz` are exposed; PostgreSQL and `/metrics` stay private ([network exposure](architecture/security.md#network-exposure)).
2. **Request → route.** Every route declares who may call it, deny by default. The route × principal matrix is [`api/authz.yaml`](../api/authz.yaml); CI fails when a route, a spec operation or the matrix disagree, and an integration test calls every route as every principal ([authorization](architecture/security.md#authorization)).
3. **Vitamux → providers and AI services.** Outbound calls carry only what the operation needs; responses are parsed with limits and fail closed on unexpected shapes ([connectors](architecture/connectors.md)).
4. **Application → database.** The app role has DML only; secrets that matter are encrypted with a key the database never sees ([database roles](architecture/security.md#database-roles), [keys](architecture/security.md#keys-and-secrets)).

## Controls

| Threat | Control | Details |
| --- | --- | --- |
| Password guessing, stolen session | argon2id, optional TOTP, login and password-change throttling, server-side sessions with idle and absolute expiry, session list and revoke, password change ends other sessions | [Owner sign-in](architecture/security.md#owner-sign-in) |
| Cross-site requests | `SameSite=Strict` cookie plus `X-CSRF-Token` on every mutating session request; bearer tokens are never sent by browsers | [Owner sign-in](architecture/security.md#owner-sign-in) |
| Over-broad machine access | Scoped, hashed, revocable, expiring API keys; client tokens bound to one connection | [api.md](architecture/api.md#conventions) |
| Exposed route by mistake | Deny-by-default `rt.handle`, matrix guard and generated tests | [Authorization](architecture/security.md#authorization) |
| Database dump | Provider tokens, TOTP secrets and documents encrypted under the master key; tokens stored as hashes | [Keys and secrets](architecture/security.md#keys-and-secrets) |
| Malicious uploads and payloads | Body, nesting, decompression and page limits; streaming parsers; fuzzing | [api.md](architecture/api.md#conventions) |
| XSS via extracted text | Strict CSP, framework escaping, no raw HTML | [Threat model](architecture/security.md#threat-model) |
| Secret or health data in logs | Redacting logger, route patterns instead of paths, no bodies; sentinel tests and an end-to-end redaction audit in CI | [reliability.md](architecture/reliability.md#health-logs-metrics) |
| Vulnerable or malicious dependency | Lockfiles, SHA-pinned Actions, digest-pinned base images, weekly grouped update PRs, govulncheck, npm audit, Trivy, SBOM | [Supply chain](#supply-chain) |
| Spoofed webhooks or OAuth replies | Per-connection hook tokens; signed, single-use, session-bound OAuth `state`; payloads only trigger a fetch | [Threat model](architecture/security.md#threat-model) |
| Data loss or theft from backups | Documented backups and restore drill; the master key is kept apart | [operations/backup.md](operations/backup.md) |

## Supply chain

- **Pins.** Go modules and tools in `go.mod`/`go.sum`, npm in `web/package-lock.json`, every GitHub Action by commit SHA (version in a comment), Dockerfile base images by multi-arch digest.
- **Updates.** [Dependabot](../.github/dependabot.yml) opens one grouped PR per ecosystem (Go, npm production, npm development, Actions, base images) each Monday, plus a daily upstream group per sidecar. Review them like code; look harder at new maintainers, install scripts and majors. Dependabot PRs are exempt from the DCO check because a bot cannot sign off.
- **Scans in CI.** `govulncheck` (pinned Go tool) fails on vulnerabilities in reachable code. `npm audit --omit=dev --audit-level=high` fails on high or critical advisories in production dependencies, which ship in the SPA; findings in development-only tooling (build, lint, test) are printed as a warning and do not block, since that code never reaches the image. Trivy fails on fixable CRITICAL image CVEs. Locally: `make vulncheck`.
- **Sidecars.** Each `sidecars/<name>/` pins its upstream in a lockfile (versions and hashes) and its base images by digest, and passes the [license gate](sidecars.md#license-gate). Dependabot bumps the upstream daily after a 1-day cooldown (a pulled or malicious release is usually caught within it); only a PR that touches nothing but that sidecar's upstream lines and has green checks is auto-merged by a workflow that never runs PR code, anything else stays open as `upstream-break`. A nightly canary builds against the upstream's default branch. `ghcr.io/kaanemec/vitamux-sidecar-<name>` images are built from green merges only, carry an SBOM and a keyless signature (same `VITAMUX_SIGN_RELEASES` switch). Sidecars have no database access and a 256 MiB limit ([sidecars.md](sidecars.md#automatic-upstream-updates)).
- **SBOM.** The `image` job writes an SPDX JSON SBOM of the image (artifact `vitamux-sbom.spdx.json`). Releases carry a BuildKit SBOM attestation per platform and attach each as `vitamux-<tag>-linux-<arch>.spdx.json` ([release.yml](../.github/workflows/release.yml)).
- **Release signing.** Unless the repository variable `VITAMUX_SIGN_RELEASES` is `false`, the release workflow signs the image digest and `checksums.txt` keylessly (Sigstore, GitHub OIDC). Verify:

  ```sh
  id='^https://github.com/KaanEmec/vitamux/\.github/workflows/release\.yml@refs/tags/v'
  iss=https://token.actions.githubusercontent.com
  cosign verify --certificate-identity-regexp "$id" --certificate-oidc-issuer "$iss" ghcr.io/kaanemec/vitamux:<tag>
  cosign verify-blob --bundle checksums.txt.sigstore.json --certificate-identity-regexp "$id" --certificate-oidc-issuer "$iss" checksums.txt
  sha256sum -c checksums.txt
  ```
- **Redaction audit.** `scripts/redaction-audit.sh` (`make redaction-audit`; CI `integration` job, PostgreSQL 18) runs a real server with sentinel secrets (owner password, TOTP secret and recovery codes, API key, client token, Withings client secret, AI provider keys, master key, OAuth code, webhook token) and sentinel health values, drives sign-in, token use, ingestion, failing Withings and AI provider calls, a fake extraction and an export, then requires zero sentinel hits in server and CLI logs, `/metrics`, `audit_events`, `jobs` and `job_runs`, and zero secret hits anywhere in the database or the export.

## Residual risks

- **Health data is not encrypted by the application at rest.** Raw and normalized rows rely on disk or volume encryption, which the operator must set up.
- **A stolen session or `admin` key is full access** until it expires or is revoked. Sessions are not bound to an address or device.
- **Throttling is in memory**, so a restart resets the counters. Long passphrases and TOTP are the real defence.
- **Webhook tokens travel in the URL.** Vitamux never logs them, but a reverse proxy may; rotate the connection if proxy logs leak.
- **Enabling an AI provider sends documents to a third party** under that provider's terms.
- **Unofficial connectors can break or change behaviour** without notice; they fail closed rather than substitute data.
- **Backups are as safe as where the operator puts them**; Vitamux documents encryption but cannot enforce it.
- **A compromised host** (root, the container, or the master key file) exposes everything.

## Reviews

Every PR that touches auth, routes, ingest, documents, connectors or secrets goes through the security checklist in [`.github/pull_request_template.md`](../.github/pull_request_template.md). Re-read this page when a new actor, asset or boundary appears.
