# E02 Database foundation and migrations

Release: MVP · Depends on: E01 · [Plan index](../README.md)
Read first: [data-model#tables](../../architecture/data-model.md#tables), [security#database-roles](../../architecture/security.md#database-roles)

**Objective:** Versioned schema for identity, sources, sync, raw and canonical data with safe migrations and least-privilege roles.

**Outputs:** Embedded goose migrations, `vitamux migrate`, role bootstrap, sqlc layer, generated schema docs.

## Acceptance
- Migrations apply from empty on PostgreSQL 17 and 18.
- The app role cannot run DDL.
- `serve` refuses a mismatched schema.
- Generated schema docs match the migrations.

## Parallelism
- Runs in parallel with E04 (J04.1–J04.3).

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J02.1](J02.1-migration-tooling.md) | Migration tooling and roles | J01.2 | None |
| [J02.2](J02.2-identity-schema.md) | Identity, settings and audit schema | J02.1 | None |
| [J02.3](J02.3-source-sync-schema.md) | Source registry and sync-state schema | J02.1 | None |
| [J02.4](J02.4-raw-canonical-schema.md) | Raw and canonical schema | J02.3 | None |
| [J02.5](J02.5-query-layer.md) | Query layer conventions | J02.2, J02.3, J02.4 | None |
