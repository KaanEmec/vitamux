# E05 Ingestion contract and raw archive

Release: MVP · Depends on: E02, E03, E04 · [Plan index](../README.md)
Read first: [connectors#push-ingest-contract](../../architecture/connectors.md#push-ingest-contract), [connectors#runtime-responsibilities](../../architecture/connectors.md#runtime-responsibilities)

**Objective:** Durable, idempotent, raw-first ingestion shared by every source mode.

**Outputs:** Ingest schemas v1, blob store, raw payload service, push ingest API with heartbeat.

## Acceptance
- Replaying any batch is a no-op; changed content creates a new raw version.
- Raw data is durable before `202`.
- Orphan blobs are swept.
- Gate G3 passed.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J05.1](J05.1-ingest-schemas.md) | Ingest schemas v1 | J01.1 | G3 |
| [J05.2](J05.2-blob-store.md) | Blob store (ADR-004) | J02.4, J03.1 | None |
| [J05.3](J05.3-raw-service.md) | Raw payload service | J05.2 | None |
| [J05.4](J05.4-push-api.md) | Push ingest API and heartbeat | J05.1, J05.3, J03.3 | None |
