# E04 Synthetic fixtures and test harness

Release: MVP · Depends on: E01 · [Plan index](../README.md)
Read first: [project#synthetic-fixtures-policy](../../architecture/project.md#synthetic-fixtures-policy), [project#testing-levels](../../architecture/project.md#testing-levels)

**Objective:** Deterministic synthetic data and shared test infrastructure for all later epics, plus a volume and performance baseline.

**Outputs:** `tools/fixturegen`, fixture privacy guard, integration harness and fakes, benchmark report.

## Acceptance
- Same seed → byte-identical output.
- CI blocks non-synthetic fixtures.
- Benchmark report exists; ADR-005 confirmed or revised.

## Parallelism
- J04.1–J04.3 run in parallel with E02; J04.4 needs J02.4.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J04.1](J04.1-fixturegen.md) | Synthetic world generator | J01.2 | None |
| [J04.2](J04.2-privacy-guard.md) | Fixture privacy guard | J01.5 | None |
| [J04.3](J04.3-integration-harness.md) | Integration harness and fakes | J02.1 | None |
| [J04.4](J04.4-volume-baseline.md) | Volume and performance baseline | J02.4, J02.5, J04.1 | None |
