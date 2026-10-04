# ADR-0018 Garmin upstream: python-garminconnect in a sidecar

Status: Accepted · Date: 2026-10-04 · Deciders: owner

## Context
Garmin Connect has no public API for personal data except the Health API, which needs an approved developer program ([E18](../plan/E18-garmin/README.md)). Vitamux reaches unofficial sources through a sidecar wrapping an existing library ([connectors#third-party-collectors](../architecture/connectors.md#third-party-collectors)), so the upstream must offer on-demand fetching, MFA sign-in, an in-memory token string, and raw JSON access, and must be maintained: Garmin changes its sign-in flow without notice.

## Decision
Wrap [`python-garminconnect`](https://github.com/cyberjunky/python-garminconnect) (PyPI `garminconnect`, MIT, 0.3.x) in `sidecars/garmin`. Pin it exactly, follow releases automatically through [J17.5](../plan/E17-sidecar-connectors/J17.5-upstream-tracking.md), and use only its generic `connectapi` and `download` calls so responses stay verbatim. Details and verified facts: [providers/garmin.md](../providers/garmin.md), [UPSTREAM.md](../../sidecars/garmin/UPSTREAM.md).

Why it fits (checked on 0.3.17, 2026-10-04): releases every one to two weeks since 0.3.0 (2026-04-02); its own mobile SSO login with MFA (`return_on_mfa`, `resume_login`); `dumps()` and `loads()` of the token set as a string; a generic `connectapi(path, params)` that returns the JSON as is; typed exceptions for 429 and authentication; a dependency tree under MIT, Apache-2.0, BSD and MPL-2.0 (`certifi`, unmodified).

## Alternatives considered
- **garth**: the earlier de facto library; its author deprecated it ("Garmin changed their auth flow") and its last release is 0.8.0 (2026-03-28).
- **GarminDB**: a complete tool with its own scheduler and SQLite storage, GPL-2.0. It could only be a push collector, would store data outside PostgreSQL first, and its license needs review before it ships next to the MIT core.
- **Official Garmin Health API**: stable and push-based, but requires an approved developer agreement, which an individual self-hoster does not have. Kept as a future second connector if access becomes realistic.
- **Own client in Go**: reimplementing SSO, Cloudflare-tolerant TLS and 150 endpoints has no advantage over reusing a maintained library behind the connector contract.

## Consequences
- Unofficial: `Official=false`, the UI shows the warning, and shape changes surface as `schema_drift` for that connection only.
- Breaking upstream releases are held by the J17.5 gate; the sidecar touches one private upstream method (`Client._refresh_di_token`), listed in UPSTREAM.md, so a bump that moves it fails the tests here first.
- A pending MFA sign-in cannot be serialized and lives in sidecar memory for 10 minutes; a restart in between means signing in again.

Switch when upstream has had no release for 90 days while sign-in is broken, when a fork under a maintained name appears and passes the same checks, or when Garmin offers personal access to the Health API.
