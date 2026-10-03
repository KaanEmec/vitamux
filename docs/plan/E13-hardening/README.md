# E13 Security and privacy hardening

Release: MVP · Depends on: E05, E06, E07, E08, E09, E10, E11, E12 · [Plan index](../README.md)
Read first: [security#threat-model](../../architecture/security.md#threat-model), [reliability#backup-and-restore](../../architecture/reliability.md#backup-and-restore)

**Objective:** Verify and harden the whole MVP against the threat model before release.

**Outputs:** Threat model doc, authorization matrix tests, fuzzing, container hardening, data lifecycle, backup/restore, supply chain and redaction audit.

## Acceptance
- All job DoDs pass in CI.
- A documented review of the release candidate finds no open high-severity issue.

## Parallelism
- J13.1 may start right after E03.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J13.1](J13.1-threat-model-authz.md) | Threat model and authorization matrix | J10.4, J12.4 | None |
| [J13.2](J13.2-fuzzing-limits.md) | Input hardening and fuzzing | J05.4, J09.1, J12.1 | None |
| [J13.3](J13.3-containers.md) | Container and Compose hardening | J01.5 | None |
| [J13.4](J13.4-data-lifecycle.md) | Data lifecycle operations | J07.4, J12.1 | None |
| [J13.5](J13.5-backup-restore.md) | Backup and restore | J05.2, J07.4 | None |
| [J13.6](J13.6-supply-chain-redaction.md) | Supply chain and redaction audit | J11.6, J12.6 | None |
