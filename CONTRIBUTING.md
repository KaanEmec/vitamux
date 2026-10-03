# Contributing to Vitamux

Thanks for helping! Start with [`docs/README.md`](docs/README.md); work items are in [`docs/plan/`](docs/plan/README.md).

## Ground rules

- **No real health data, account identifiers, or credentials** anywhere: code, fixtures, issues, logs, screenshots. Fixtures come from `tools/fixturegen` and are synthetic only.
- Vitamux never diagnoses or interprets health data.
- Keep docs lean: change the smallest relevant file and link instead of repeating.
- Decisions that code depends on get an ADR ([`docs/adr/`](docs/adr/README.md)).

## Workflow

1. Pick or open an issue and find its plan job; read only the linked architecture sections.
2. Branch from `main`, make focused commits ([Conventional Commits](https://www.conventionalcommits.org/) style, e.g. `feat(ingest): …`). Subjects become the release notes and [`CHANGELOG.md`](CHANGELOG.md) (`scripts/release-notes.sh`), so write them for users; mark breaking changes with `!` or a `BREAKING CHANGE:` footer that says how to upgrade.
3. Run `make lint test` (and `make test-integration` when touching the database).
4. Open a PR and complete the checklist in the template.

## Developer Certificate of Origin (DCO)

Every commit must carry a sign-off certifying the [DCO](https://developercertificate.org/):

```text
Signed-off-by: Your Name <you@example.com>
```

Use `git commit -s`. CI rejects PRs with unsigned commits. There is no CLA. Contributions are licensed under the project's [MIT license](LICENSE).

When you change Go or npm dependencies, run `make notices` and commit `THIRD_PARTY_NOTICES.md`; CI rejects drift and licenses outside the allowlist.

## Unofficial provider adapters

Adapters for reverse-engineered APIs must be optional, isolated, exactly pinned, and clearly labelled unofficial. Their dependency bumps always need manual review.
