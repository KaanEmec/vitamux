# E01 Repository bootstrap and contributor experience

Release: MVP · Depends on: G0 · [Plan index](../README.md)
Read first: [overview#key-decisions](../../architecture/overview.md#key-decisions), [overview#repository-layout](../../architecture/overview.md#repository-layout), [project#versioning-and-releases](../../architecture/project.md#versioning-and-releases)

**Objective:** A repository a contributor can clone, run and test with one-line commands, with foundational decisions recorded as ADRs.

**Outputs:** Go module and `vitamux` skeleton, embedded SvelteKit scaffold, dev Compose, CI, community files, accepted ADRs.

## Acceptance
- `make dev` works on clean macOS and Linux; `/healthz` = 200 and placeholder UI served.
- CI runs lint, unit, integration, web build, image build, secret scan and license report.
- Gate G1 passed.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J01.1](J01.1-foundational-adrs.md) | Foundational ADRs | G0 | G1 |
| [J01.2](J01.2-go-skeleton.md) | Go module, binary skeleton, config, boundaries | J01.1 | None |
| [J01.3](J01.3-dev-environment.md) | Local development environment | J01.2 | None |
| [J01.4](J01.4-web-scaffold.md) | Web scaffold and embedding | J01.2 | None |
| [J01.5](J01.5-ci-pipeline.md) | CI pipeline | J01.2, J01.3, J01.4 | None |
| [J01.6](J01.6-community-files.md) | Community files | J01.1 | None |
