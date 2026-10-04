# ADR-0019 WHOOP through the unofficial `@dofek/whoop` client, in a sidecar

Status: Accepted · Date: 2026-10-04 · Deciders: owner

## Context
Vitamux wants WHOOP's 6-second heart rate, recovery inputs (HRV, resting heart rate, SpO2, skin temperature), sleep stages and strength detail ([E19](../plan/E19-whoop/README.md)). WHOOP's official Developer Platform (OAuth, documented, supported) exposes scored summaries: cycles, recovery, sleep and workout scores, body measurements. As far as reviewed, it has no continuous heart-rate series and no per-stage sleep timeline or exercise-level strength data. The app's private API has all of them, and an open-source client exists: [`@dofek/whoop`](https://github.com/Asherlc/dofek/tree/main/packages/whoop-whoop) (TypeScript, Cognito sign-in with app or SMS MFA, token refresh, the data methods). Third-party collectors run as sidecars behind the connector contract ([connectors.md](../architecture/connectors.md#third-party-collectors)); facts about the API are in [providers/whoop.md](../providers/whoop.md).

## Decision
- Wrap `@dofek/whoop` in a stateless Node sidecar ([`sidecars/whoop`](../../sidecars/whoop)) that speaks `vitamux-connector/1`. The connector is `Official=false`, `auth_kind interactive_mfa`, starts disabled, and reports a changed shape as `schema_drift`.
- Keep raw responses byte-exact: the sidecar captures the response text through the client's injectable `fetch` instead of using the client's parsed return values. Normalisation stays in Go.
- Pin `@dofek/whoop` exactly in `package.json` and `package-lock.json`. Upstream releases are adopted automatically once the sidecar tests and the connector conformance test pass, and held when one fails ([J17.5](../plan/E17-sidecar-connectors/J17.5-upstream-tracking.md)).
- Licenses: the whole runtime tree is MIT (`@dofek/whoop`, `@dofek/provider-http`, `@dofek/training`, `@dofek/scoring`, `@dofek/zones`, `@openbeta/sandbag`, `zod`), checked 2026-10-04. The sidecar is a separate container, so the core stays MIT with no combined-work concerns; `UPSTREAM.md` records repo, license and status.
- Switch or add the official API when any of these holds: WHOOP's official API gains the data Vitamux needs (an official connector then replaces the sidecar stream by stream, with the same normalizers where fields match); the upstream is unmaintained or its breakage stays unfixed for more than 30 days; WHOOP removes password sign-in or the private endpoints; the owner decides the terms-of-service risk is no longer acceptable. Vitamux does not fork the upstream; a fix goes upstream first.

## Alternatives considered
- **Official WHOOP API only**: supported and stable, but summaries only; it loses the heart-rate series and sleep stages that motivate the connector. It remains a possible second connector for summaries.
- **Port the client to Go**: removes the second runtime, but means maintaining our own reverse-engineered Cognito and endpoint code with no upstream to share breakage with. The sidecar costs one small container.
- **Run Dofek itself as a push collector**: it brings its own database, scheduler and schema, against "raw first, one PostgreSQL, our normalizers".
- **Use the client's parsed return values as raw**: simpler, but `getCycles`, `getMetricValues` and `listDeveloperWorkouts` unwrap or rebuild the body, so the raw would not be what WHOOP sent and could not be reprocessed faithfully.

## Consequences
- **Lockstep releases**: the `@dofek/*` packages share one version and pin each other exactly (and `zod` exactly), so the tree moves as a unit: one bump means one lockfile change, and a partial bump is not possible. Every upstream release is a possible behaviour change in a private API; the conformance test and the shape fingerprints are the gate, and a held bump leaves the pinned version running.
- The upstream releases often (0.1.65 at the time of writing, version 0.x); the exact pin and the 30-day rule above bound that risk.
- Private API risk is accepted and shown to the owner: it can break without notice, and using it sits outside WHOOP's developer terms. The connector is opt-in, paced at one request per second, and only for the owner's own account.
- Heart rate at 6 s is about 14,400 samples per day: the default backfill is capped (90 days) and retention settings matter ([providers/whoop.md](../providers/whoop.md#how-vitamux-syncs)).
- Credentials (access and refresh token, user id) are sealed by the core; the sidecar and its logs never hold or print them, nor passwords, MFA codes, MFA sessions or response bodies.
- Follow-up: J19.4 normalizers against the raw envelope (`{"unit", "response"}`), J19.5 lifecycle, an owner shape check, and a redaction audit with sentinel WHOOP secrets.
