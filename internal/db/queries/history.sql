-- Sync observability (J06.7); see docs/architecture/reliability.md#health-logs-metrics.

-- name: ListRecentJobRuns :many
-- Newest runs first, optionally of one connection and/or job kind. error_message is already sanitized.
SELECT r.id, r.job_id, j.kind, j.connection_id, r.attempt, r.started_at, r.finished_at,
       r.outcome, r.error_class, r.error_message, r.stats
FROM job_runs r JOIN jobs j ON j.id = r.job_id
WHERE (sqlc.narg(connection_id)::uuid IS NULL OR j.connection_id = sqlc.narg(connection_id))
  AND (sqlc.narg(kind)::text IS NULL OR j.kind = sqlc.narg(kind))
ORDER BY r.id DESC
LIMIT @row_limit;

-- name: CountJobsByKindStatus :many
-- Scrape-time gauge source: queue depth per kind and status, and the age of the oldest due job.
SELECT kind, status, count(*) AS n,
       coalesce(extract(epoch FROM now() - min(run_at) FILTER (WHERE status = 'queued' AND run_at <= now())), 0)::float8 AS oldest_due_seconds
FROM jobs
WHERE status IN ('queued', 'running', 'dead')
GROUP BY kind, status;

-- name: ListConnectionMetrics :many
-- Scrape-time gauge source: one row per connection.
SELECT c.id, p.code AS provider, c.status, c.last_success_at
FROM connections c JOIN providers p ON p.id = c.provider_id;
