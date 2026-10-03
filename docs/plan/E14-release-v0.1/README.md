# E14 Open-source docs, licensing and v0.1.0 release (MVP)

Release: MVP · Depends on: E01, E02, E03, E04, E05, E06, E07, E08, E09, E10, E11, E12, E13 · [Plan index](../README.md)
Read first: [project#license-and-notices](../../architecture/project.md#license-and-notices), [project#versioning-and-releases](../../architecture/project.md#versioning-and-releases), [project#resource-budget](../../architecture/project.md#resource-budget)

**Objective:** Publish a documented, installable, licensed MVP.

**Outputs:** Licenses and notices, documentation set, resource measurements, release pipeline, clean-machine validation, v0.1.0.

## Acceptance
- Following only the docs, v0.1.0 installs on a clean Linux VM and macOS, connects the fake/demo provider, shows resolved data and restores a backup.
- Images, SBOMs, checksums and the Compose bundle are published.
- Measured resources are within budget or the docs explain the deviation.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J14.1](J14.1-licensing.md) | Licensing and notices | J01.6 | None |
| [J14.2](J14.2-docs-set.md) | Documentation set | J13.6 | None |
| [J14.3](J14.3-resource-budget.md) | Resource budget verification | J13.6 | None |
| [J14.4](J14.4-release-engineering.md) | Release engineering and clean-install validation | J01.5, J13.6, J14.2 | None |
| [J14.5](J14.5-release.md) | v0.1.0 release | J14.1, J14.2, J14.3, J14.4 | G4 |
