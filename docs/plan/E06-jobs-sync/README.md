# E06 Jobs, scheduling and sync state

Release: MVP · Depends on: E02, E03, E05 · [Plan index](../README.md)
Read first: [reliability#job-queue](../../architecture/reliability.md#job-queue), [reliability#scheduler](../../architecture/reliability.md#scheduler), [connectors#runtime-responsibilities](../../architecture/connectors.md#runtime-responsibilities)

**Objective:** PostgreSQL job system, scheduler, connector runtime, rate limiting and backfills.

**Outputs:** `internal/jobs`, `internal/connectors/runtime`, `ratelimit`, backfill planner, sync observability.

## Acceptance
- No duplicate effects under concurrent workers, crashes or two schedulers.
- Every typed error maps to its documented state.
- Backfills resume after restart.

## Parallelism
- Runs in parallel with E07 once J05.3 is done.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J06.1](J06.1-job-queue.md) | Job queue core (ADR-003) | J02.3 | None |
| [J06.2](J06.2-reaper-shutdown.md) | Reaper and graceful shutdown | J06.1 | None |
| [J06.3](J06.3-scheduler.md) | Scheduler leader and schedules | J06.1 | None |
| [J06.4](J06.4-connector-runtime.md) | Connector runtime | J06.1, J05.3, J03.1 | None |
| [J06.5](J06.5-rate-limiting.md) | Rate limiting and backoff | J06.4 | None |
| [J06.6](J06.6-backfill.md) | Backfill planner | J06.4 | None |
| [J06.7](J06.7-sync-observability.md) | Sync observability | J06.4 | None |
