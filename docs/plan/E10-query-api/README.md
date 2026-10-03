# E10 Query and configuration API

Release: MVP · Depends on: E03, E06, E07, E09 · [Plan index](../README.md)
Read first: [api#conventions](../../architecture/api.md#conventions), [api#owner-endpoints-apiv1](../../architecture/api.md#owner-endpoints-apiv1)

**Objective:** A documented, contract-tested owner API for source data, resolved data, configuration, coverage, health and exports.

**Outputs:** `api/openapi.yaml` v1, handlers, generated server types and TS client, API reference, export pipeline.

## Acceptance
- Every route is in the spec and contract-tested.
- The api.md examples validate.
- Export → import round-trip preserves data and provenance.

## Parallelism
- Areas proceed as their dependencies land; E11 follows area by area.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J10.1](J10.1-openapi-skeleton.md) | OpenAPI skeleton and codegen | J03.5 | None |
| [J10.2](J10.2-source-endpoints.md) | Source data and provenance endpoints | J10.1, J07.6 | None |
| [J10.3](J10.3-resolved-endpoints.md) | Resolved endpoints and preview | J10.1, J09.9 | None |
| [J10.4](J10.4-config-endpoints.md) | Configuration endpoints | J10.1, J09.2, J09.7, J06.3, J06.6 | None |
| [J10.5](J10.5-coverage-status.md) | Coverage and system status | J10.1, J06.7 | None |
| [J10.6](J10.6-exports.md) | Exports and NDJSON import | J10.1, J06.1 | None |
