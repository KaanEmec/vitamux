-- Coverage and system status (J10.5).

-- name: CoverageHours :many
-- Hours with data per provider, metric and local date, from the hourly aggregates. A provider
-- counts an hour once however many of its devices and apps contributed. The hour_start bounds
-- (the date range widened by a day on both sides) let the primary key narrow the scan; local_date
-- is the exact filter. origins (origin keys) narrows to the hours those apps contributed.
SELECT mc.code AS metric, p.code AS source, a.local_date, count(DISTINCT a.hour_start)::int AS hours
FROM source_hourly_aggregates a
JOIN metric_catalog mc ON mc.id = a.metric_id
JOIN connections c ON c.id = a.connection_id
JOIN providers p ON p.id = c.provider_id
WHERE a.user_id = @user_id
  AND a.hour_start >= @from_at AND a.hour_start < @to_at
  AND a.local_date BETWEEN @from_date AND @to_date
  AND (sqlc.narg(metrics)::text[] IS NULL OR mc.code = ANY(sqlc.narg(metrics)::text[]))
  AND (sqlc.narg(origins)::text[] IS NULL OR a.origin_id IN
       (SELECT o.id FROM data_origins o WHERE o.user_id = @user_id AND o.origin_key = ANY(sqlc.narg(origins)::text[])))
GROUP BY mc.code, p.code, a.local_date
ORDER BY mc.code, p.code, a.local_date;

-- name: InstanceSizes :one
-- Database size, Postgres version and the stored size of the live blobs.
SELECT pg_database_size(current_database())::bigint AS database_bytes,
       current_setting('server_version')::text AS postgres_version,
       (SELECT coalesce(sum(stored_bytes), 0)::bigint FROM blobs) AS blob_bytes;

-- name: ListDeadJobsSince :many
-- Jobs that ran out of attempts since @since, newest first, with the error class of their last run.
-- Connection-less jobs belong to the single owner.
SELECT j.id, j.kind, j.connection_id, j.attempts, j.finished_at, r.error_class
FROM jobs j
LEFT JOIN connections c ON c.id = j.connection_id
LEFT JOIN LATERAL (SELECT jr.error_class FROM job_runs jr WHERE jr.job_id = j.id ORDER BY jr.id DESC LIMIT 1) r ON true
WHERE j.status = 'dead' AND j.finished_at >= @since
  AND (j.connection_id IS NULL OR c.user_id = @user_id)
ORDER BY j.finished_at DESC, j.id DESC
LIMIT @lim;
