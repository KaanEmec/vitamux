# E07 Normalization framework and provenance

Release: MVP · Depends on: E02, E05 · [Plan index](../README.md)
Read first: [connectors#normalizer-contract](../../architecture/connectors.md#normalizer-contract), [data-model#principles](../../architecture/data-model.md#principles), [data-model#identifiers-and-dedupe-keys](../../architecture/data-model.md#identifiers-and-dedupe-keys)

**Objective:** Deterministic, versioned normalization into the canonical model with provenance and correction semantics.

**Outputs:** Metric catalogue and units, local-date service, normalizer framework, canonical writer, normalize and reprocess jobs, provenance service.

## Acceptance
- Replaying raw 3× gives an identical active dataset.
- A changed value gives exactly one superseded row and one new row.
- Every canonical row traces to raw, batch, connection/client and normalizer.
- CI fails on output change without a version bump.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J07.1](J07.1-catalogue-units.md) | Metric catalogue and units v1 | J02.4 | None |
| [J07.2](J07.2-local-date.md) | Time and local date | J02.2 | None |
| [J07.3](J07.3-normalizer-framework.md) | Normalizer framework and golden tests | J07.1 | None |
| [J07.4](J07.4-canonical-writer.md) | Canonical writer (ADR-016) | J07.3, J07.2 | None |
| [J07.5](J07.5-normalize-reprocess.md) | normalize_batch job and reprocess | J07.4, J06.1 | None |
| [J07.6](J07.6-provenance.md) | Provenance service | J07.4 | None |
