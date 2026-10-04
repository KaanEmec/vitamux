# E09 Resolution engine

Release: MVP · Depends on: E04, E07, G2 · [Plan index](../README.md)
Read first: [resolution#rule-specification](../../architecture/resolution.md#rule-specification), [resolution#strategies](../../architecture/resolution.md#strategies), [resolution#edge-cases](../../architecture/resolution.md#edge-cases)

**Objective:** Typed, versioned, per-metric and per-window resolution with fallback and cross-source calculations, full provenance, explanations and a rebuildable cache.

**Outputs:** `internal/resolve`, resolution tables, built-in defaults, scenario and property suites.

## Acceptance
- All resolution edge cases pass as scenario tests.
- The MVP rule extensions (E1, E2, E3, E5, E9) pass their scenarios.
- Property tests pass.
- `vitamux resolve verify` shows 0 diffs on 1,000 random windows.
- J09.9 performance targets met.

## Parallelism
- Runs in parallel with E08 on synthetic canonical data.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J09.1](J09.1-rule-schema.md) | Rule schema v1 and validator | J07.1 | G2 |
| [J09.2](J09.2-rules-storage-defaults.md) | Built-in defaults, rule storage and versioning | J09.1, J03.4 | None |
| [J09.3](J09.3-selectors-windows.md) | Source selectors, grouping and windows | J09.1, J07.2, J07.4 | None |
| [J09.4](J09.4-within-source.md) | Within-source aggregation | J09.3 | None |
| [J09.5](J09.5-strategies.md) | Strategies, quality gates and fallback | J09.4 | None |
| [J09.6](J09.6-alignment.md) | Sleep and workout alignment | J09.3 | None |
| [J09.7](J09.7-overrides.md) | Manual overrides | J09.5, J03.4 | None |
| [J09.8](J09.8-results-scenarios.md) | Results, explanations and scenario suite | J09.5, J09.6, J09.7, J09.10 | None |
| [J09.9](J09.9-cache.md) | Materialization and cache | J09.8, J04.4 | None |
| [J09.10](J09.10-rule-extensions.md) | Context ladders, window statistics, wear gate, coherence, hour composition (E1, E2, E3, E5, E9) | J09.5, J09.6 | None |
| [J09.11](J09.11-brand-device-selectors.md) | Brand and device choices in rules, defaults rebuilt on them | J09.3, J09.2, J20.7 | None |
