-- Sync observability (J06.7); see docs/architecture/reliability.md#health-logs-metrics.

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
