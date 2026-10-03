# ADR-0015 REST + OpenAPI 3.1 contract-first API conventions

Status: Accepted · Date: 2026-10-03 · Deciders: owner (G0 approval)

## Context
The UI, scripts, collectors, and the iOS app share one API, which needs a single machine-checkable contract. See [api.md › Conventions](../architecture/api.md#conventions).

## Decision
- `api/openapi.yaml` (OpenAPI 3.1) is the source of truth. Server types come from `oapi-codegen` (strict server), the TS client from `openapi-typescript`, and the spec is linted by Spectral.
- `/api/v1` is for the owner and `/api/ingest/v1` for clients and devices. Changes within v1 are additive only.
- RFC 3339 instants with offset over half-open ranges; local dates as `YYYY-MM-DD`.
- Opaque, HMAC-protected cursor pagination. No offsets.
- Errors are `application/problem+json` with stable `code` values.
- `Idempotency-Key` on ingest and job-creating POSTs.

## Alternatives considered
- **GraphQL**: flexible, but harder to cache and secure, and too much for a small fixed UI.
- **Code-first spec generation**: less upfront work, but the contract drifts from intent.

## Consequences
- Contract tests validate every response (J10.x).
- Generated code is checked in CI for drift.
