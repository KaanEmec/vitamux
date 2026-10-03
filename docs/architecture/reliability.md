# Jobs, scheduling, reliability, operations

## Job queue

Decision and handler contract: [ADR-0003](../adr/0003-postgres-job-queue.md).

```sql
WITH next AS (
  SELECT j.id FROM jobs j
  WHERE j.status = 'queued' AND j.run_at <= now()
    AND NOT (j.exclusive AND EXISTS (SELECT 1 FROM jobs r
             WHERE r.connection_id = j.connection_id AND r.status = 'running' AND r.exclusive))
  ORDER BY j.priority, j.run_at
  FOR UPDATE SKIP LOCKED LIMIT 1)
UPDATE jobs SET status='running', lease_owner=$1, lease_expires_at=now()+interval '2 minutes',
       attempts=attempts+1, started_at=now()
FROM next WHERE jobs.id = next.id RETURNING jobs.*;
```

- `UNIQUE (connection_id) WHERE status='running' AND exclusive` gives one sync per connection; a racing claim fails and retries.
- `UNIQUE (dedupe_key) WHERE status IN ('queued','running')` makes enqueue idempotent for schedule slots, double-clicks, and webhook bursts.
- Heartbeats extend the lease every 30 s. The reaper requeues expired leases (counting an attempt) and moves jobs to `dead` after `max_attempts`.
- Retry delay = `min(cap, base·2^attempt)·U(0.5,1)`. `Retry-After` takes precedence. Rate-limit reschedules do not count as attempts; auth failures are not retried.
- `LISTEN/NOTIFY` wakes workers on enqueue, with a 5 s polling fallback.
- Job kinds: `sync`, `backfill_unit`, `normalize_batch`, `reprocess`, `rebuild_aggregates`, `extract_document`, `export`, `backup`, `import_unit`, `sweep_blobs`, `recompute_local_dates`, `document_retention`, `prune_raw`, `prune_superseded`, `prune_idempotency_keys` (daily retention jobs: [data-model.md](data-model.md#retention)).

## Scheduler

- One leader per database via `pg_try_advisory_lock` on a dedicated connection. Failover happens automatically.
- Every 15 s the leader turns due `schedules` into jobs with `dedupe_key = schedule_id:slot`. Even two leaders cannot duplicate jobs.
- Slots missed while no leader ran coalesce into one job. A schedule's `mode` is `incremental` (follow the cursor) or `correction` (re-fetch the `lookback` window ending at the slot).
- Defaults come from connector descriptors (Withings measures: hourly). They are editable per stream.

## Idempotency

Jobs are at-least-once; their effects are idempotent:

- raw rows are unique on content;
- canonical rows are unique on the dedupe key, with supersession;
- cursors commit with their raw rows;
- enqueue is deduplicated.

Re-running a job or re-importing a file changes nothing.

## Upgrades

1. `vitamux migrate up` runs as a one-shot container before `serve`. It holds an advisory lock and follows expand/contract migrations.
2. On `SIGTERM` the process stops claiming, finishes or checkpoints within 60 s, then releases its leases. The new process resumes from cursors and backfill units.
3. `serve` refuses to run on a newer schema (downgrade protection) or an un-migrated schema.

## Health, logs, metrics

- `/healthz`: process alive. `/readyz`: DB reachable, schema matches, blob volume writable, worker heartbeat < 60 s old, master key loaded.
- Logs are `slog` JSON with `request_id`, `job_id`, `connection_id`, `provider`, `stream`, `error_class`. A redacting handler drops `token|secret|password|authorization|cookie|code|refresh`-like attributes. Bodies of provider calls, ingest payloads, documents, and AI responses are never logged. The [redaction audit](../security.md#supply-chain) proves this end to end in CI.
- Metrics (`internal/metrics`) on the private listener `VITAMUX_METRICS_ADDR` (e.g. `127.0.0.1:9090`; empty disables it), serving `/metrics` only and never mounted on the public mux. Built so far: `vitamux_jobs{kind,status}`, `vitamux_job_queue_age_seconds`, `vitamux_job_runs_total{kind,outcome}`, `vitamux_job_duration_seconds`, `vitamux_sync_pages_total`, `vitamux_sync_raw_items_total{outcome}`, `vitamux_provider_rate_limit_blocks_total`, `vitamux_connections{provider,status}`, `vitamux_connection_last_success_timestamp_seconds`, plus Go runtime. Health words come from `connectors.DeriveHealth`. Planned:
  - jobs by kind and status, durations, queue age;
  - provider requests, throttles, and errors;
  - raw items stored, duplicate, or new version;
  - canonical rows inserted or superseded; normalize failures; quarantined payloads;
  - cache hit ratio; last success per connection; DB pool; Go runtime.
- The UI System page shows the same information, so no monitoring stack is required.

## Backup and restore

- `vitamux backup --out DIR` writes (operator guide: [operations/backup.md](../operations/backup.md)):
  1. `pg_dump -Fc` (the image ships a matching client);
  2. a tar of the blobs and `names.key` (content-addressed and immutable, so copying after the dump guarantees every referenced blob exists; [ADR-0004](../adr/0004-blob-store.md));
  3. `manifest.json` (versions, sha256 checksums, blob count, required master `key_id`, a MAC under the master key).

  It can also run as a scheduled `backup` job with a retention count. Off-host encryption (restic, age, rclone) is documented but not built in.
- `vitamux restore --from DIR` restores into an empty DB and copies blobs. It verifies the manifest and blob references, refuses on any checksum mismatch, and migrates an older schema. Run `vitamux resolve verify` afterwards.
- The master key is never in the bundle and must be backed up separately.
- A restore drill runs in CI on synthetic data.
- Backup vs export: a backup is the whole instance (secrets, jobs, sealed documents) for disaster recovery and needs the master key. An [export](api.md#exports) is the owner's data as portable NDJSON without secrets, readable by other tools and importable into a fresh or existing instance of the same schema version; it is not a backup.
