# ADR-0003 Own PostgreSQL job queue and scheduler

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Syncs, backfill units, normalization and maintenance must run at least once, survive crashes and upgrades, never run two syncs of one connection at once, and never duplicate a schedule slot, all without a broker ([ADR-0002](0002-modular-monolith.md)). Mechanics and defaults: [reliability.md › Job queue](../architecture/reliability.md#job-queue), [› Scheduler](../architecture/reliability.md#scheduler), [› Upgrades](../architecture/reliability.md#upgrades).

## Decision
`internal/jobs` implements a small queue on the `jobs`/`job_runs`/`schedules` tables:

- **Enqueue** (`jobs.Enqueue(ctx, q, NewJob)`) takes the caller's `*dbq.Queries`, so a job can be enqueued in the same transaction as the write that needs it. `ON CONFLICT DO NOTHING` on the active-dedupe index returns the existing job. `pg_notify('vitamux_jobs')` wakes workers on commit; they also poll every 5 s.
- **Claim** is the `FOR UPDATE SKIP LOCKED` query from reliability.md, restricted to kinds with a registered handler. It increments `attempts` and opens a `job_runs` row. A racing claim of a second exclusive job for one connection hits the unique index and simply claims again.
- **Fencing**: every later write of a running job (heartbeat, checkpoint, outcome) matches `lease_owner` and `attempts`. A worker whose lease was reaped changes nothing and is told `ErrLeaseLost`.
- **Handlers** are `func(ctx, Job) error`, registered per kind on a `Runner`. They return nil, `RescheduleAt(t, cause)` (requeue without consuming an attempt, for Retry-After), `Permanent(err)` (dead now), or any other error (retry after `RetryDelay`, dead after `max_attempts`). An error with an `ErrorClass() string` method sets `job_runs.error_class`; messages are redacted and truncated. `Job.SaveCheckpoint` stores progress in `jobs.checkpoint`, which a retry or re-claim receives.
- **Reaper**: every process requeues expired leases each 30 s (the attempt stays counted; out of attempts → `dead`) and closes the run as `lease_expired`.
- **Shutdown**: stop claiming, wait up to 50 s, cancel handler contexts, wait up to 5 s, then requeue this owner's remaining jobs with the attempt refunded (`rescheduled`/`shutdown`). The `failed` job status is not produced; failed attempts are visible in `job_runs`.
- **Scheduler**: every process ticks every 15 s; the holder of `pg_try_advisory_lock` on a dedicated connection materializes. In one transaction it locks due schedules (`FOR UPDATE SKIP LOCKED`, active or degraded connections only), enqueues an exclusive `sync` job with dedupe key `schedule_id:slot`, and advances `next_run_at` on the fixed grid. Missed slots coalesce into the latest one; the cursor covers the gap. Schedules have a `mode`: `incremental` follows the cursor, `correction` re-fetches `[slot − lookback, slot)` at low priority.

## Alternatives considered
- **River** (Go, PostgreSQL): mature, but brings its own schema, migrations and conventions to a codebase that wants every table documented and owned; our needs fit in under 1,000 lines.
- **Redis/asynq, a broker, or a workflow engine**: an extra service, ruled out by ADR-0002.
- **Leader-only reaper**: one more thing that stops when the leader dies; the reaper is idempotent, so every process runs it.
- **Materializing every missed slot**: floods the queue after downtime with jobs the cursor already covers.

## Consequences
- Throughput is bounded by PostgreSQL row locking: thousands of jobs per minute, far above personal scale.
- Handlers must be idempotent (at-least-once) and should checkpoint long work.
- The deploy's stop timeout must exceed the 60 s drain (Compose `stop_grace_period: 60s`).
- Two dedicated connections per process (LISTEN and the leader lock) sit outside the pool.
