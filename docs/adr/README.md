# Architecture decision records

ADRs record decisions that code depends on. Keep each one short and link to the architecture doc for details. Use [0000-template.md](0000-template.md). Number ADRs as listed in [overview.md › Key decisions](../architecture/overview.md#key-decisions); the remaining ones are written by the jobs that own them.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-go-core.md) | Go core; other languages only in optional sidecars | Accepted |
| [0002](0002-modular-monolith.md) | Modular monolith, one binary with modes, PostgreSQL only | Accepted |
| [0003](0003-postgres-job-queue.md) | Own PostgreSQL job queue: `SKIP LOCKED`, fenced leases, dedupe keys, advisory-lock scheduler | Accepted |
| [0004](0004-blob-store.md) | Filesystem blob store: SHA-256 addressed, HMAC file names, zstd, verified reads, refcount sweep | Accepted |
| [0008](0008-rule-schema.md) | Typed resolution rule schema v1 (`vitamux.rule/1`), first-match groups, sleep and blood-pressure rule families | Accepted |
| [0009](0009-sleep-date-night-window.md) | `sleep_date` = local wake date; night D = sessions ending in `[D−1 18:00, D 18:00)` local | Accepted |
| [0010](0010-sveltekit-static-spa.md) | SvelteKit static SPA embedded in the binary | Accepted |
| [0011](0011-secrets-vault.md) | Master key file, HKDF purposes, AES-256-GCM sealed values with `key_id` | Accepted |
| [0012](0012-mit-license-dco.md) | MIT license with DCO sign-off | Accepted |
| [0015](0015-rest-openapi-conventions.md) | REST + OpenAPI 3.1 contract-first, problem+json, cursor pagination | Accepted |
| [0016](0016-canonical-writer.md) | Account-scoped dedupe keys; corrections supersede; one canonical writer | Accepted |
