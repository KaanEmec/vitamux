# ADR-0002 Modular monolith, one binary with modes, PostgreSQL only

Status: Accepted · Date: 2026-10-03 · Deciders: owner (G0 approval)

## Context
Personal-scale workload, one owner, a small community, and a goal of being much leaner than a multi-container stack with a broker and a separate scheduler. See [overview.md › Process modes](../architecture/overview.md#process-modes) and [reliability.md](../architecture/reliability.md).

## Decision
- A single binary `vitamux` with subcommands (`serve`, `migrate`, `import`, `reprocess`, `backup`, `restore`, `admin`).
- `serve` runs HTTP, the scheduler leader, and workers by default; `--roles` can split them.
- PostgreSQL is the only stateful service: data, job queue, locks, schedules, cache. Blobs live on a volume.
- Package boundaries are enforced by lint rules (J01.2).

## Alternatives considered
- **Microservices / separate worker service**: more operations and deployment surface with no evidenced need.
- **Redis or another broker for jobs**: an extra service; PostgreSQL `SKIP LOCKED` is enough at this scale (ADR-0003, J06.1).

## Consequences
- The minimal deployment is two containers (`vitamux`, `postgres`).
- New infrastructure needs an ADR backed by evidence.
