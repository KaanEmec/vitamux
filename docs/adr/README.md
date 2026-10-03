# Architecture decision records

ADRs record decisions that code depends on. Keep each one short and link to the architecture doc for details. Use [0000-template.md](0000-template.md). Number ADRs as listed in [overview.md › Key decisions](../architecture/overview.md#key-decisions); the remaining ones are written by the jobs that own them.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-go-core.md) | Go core; other languages only in optional sidecars | Accepted |
| [0002](0002-modular-monolith.md) | Modular monolith, one binary with modes, PostgreSQL only | Accepted |
| [0010](0010-sveltekit-static-spa.md) | SvelteKit static SPA embedded in the binary | Accepted |
| [0012](0012-mit-license-dco.md) | MIT license with DCO sign-off | Accepted |
| [0015](0015-rest-openapi-conventions.md) | REST + OpenAPI 3.1 contract-first, problem+json, cursor pagination | Accepted |
