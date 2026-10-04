# Upstream: @dofek/whoop

Read by the license gate (`go run ./tools/notices -check`), Dependabot automation and humans.
Rules: [docs/sidecars.md#license-gate](../../docs/sidecars.md#license-gate).

- Repository: https://github.com/Asherlc/dofek
- Package: @dofek/whoop
- Lockstep packages: @dofek/provider-http, @dofek/training, @dofek/scoring, @dofek/zones
- Version: 0.1.65
- Release tag: @dofek/provider-http@0.1.65 (commit e000b19, shared by the lockstep packages; this package has no tag of its own)
- License: MIT
- Status: unofficial
- License review: not needed, the whole tree is permissive (`@dofek/provider-http`, `@dofek/training`, `@dofek/scoring`, `@dofek/zones`, `@openbeta/sandbag`, `zod`)

## Notes

- Workaround to drop on an upstream bump: `listDeveloperWorkouts`'s zod schema refuses `null` in optional fields (`sport_id: null` on older workouts), so the sidecar falls back to its own parse of the captured page on `ZodError`. Remove it once upstream accepts nulls.

- Published on npm as [`@dofek/whoop`](https://www.npmjs.com/package/@dofek/whoop), pinned exactly in `package.json` and `package-lock.json`. Source: https://github.com/Asherlc/dofek/tree/main/packages/whoop-whoop. Runtime: Node >= 22.14.
- **Unofficial.** Client for WHOOP's private app API (Cognito sign-in). Not affiliated with or endorsed by WHOOP. Vitamux marks the connector `official: false` and a new connection starts paused.
- The `@dofek/*` siblings are released in lockstep (same version number, exact pins between them). Update them together: one `npm install @dofek/whoop@<version>` moves the tree.
- Why this upstream and not WHOOP's official API: [ADR-0019](../../docs/adr/0019-whoop-upstream.md). Verified facts: [providers/whoop.md](../../docs/providers/whoop.md).
- The canary (`UPSTREAM_GIT`) cannot install one package of a monorepo from git with npm, so it has no useful source for this sidecar.

What the sidecar uses: `WhoopClient.signIn`, `verifyCode`, `refreshAccessToken`, `getHeartRate`, `getCycles`, `getSleep`, `listDeveloperWorkouts`, `getWeightliftingWorkout`, `getStrainDeepDive`, `getJournal`, and `WHOOP_API_THROTTLE_MS`. The client's own parsing is bypassed for the raw: the sidecar keeps the response text from the injected `fetch`.
