-- Backfills (J06.6); see docs/architecture/connectors.md#runtime-responsibilities.

-- name: InsertBackfill :exec
INSERT INTO backfills (id, connection_id, stream, range_start, range_end, daily_limit)
VALUES (@id, @connection_id, @stream, @range_start, @range_end, sqlc.narg(daily_limit));

-- name: InsertBackfillUnits :exec
INSERT INTO backfill_units (backfill_id, range_start, range_end)
SELECT @backfill_id::uuid, unnest(@starts::timestamptz[]), unnest(@ends::timestamptz[]);

-- name: GetBackfillUnit :one
SELECT u.range_start, u.range_end, u.status, b.connection_id, b.stream, b.status AS backfill_status, b.daily_limit
FROM backfill_units u JOIN backfills b ON b.id = u.backfill_id
WHERE u.backfill_id = @backfill_id AND u.range_start = @range_start;

-- name: CountUnitsStartedSince :one
-- Units of a connection's stream that started (or finished) since the given time, other than one
-- unit: what a paced backfill spent of today's limit.
SELECT count(*) FROM backfill_units u JOIN backfills b ON b.id = u.backfill_id
WHERE b.connection_id = @connection_id AND b.stream = @stream AND u.status IN ('running', 'done')
  AND u.updated_at >= @since AND NOT (u.backfill_id = @backfill_id AND u.range_start = @range_start);

-- name: StartBackfillUnit :exec
UPDATE backfill_units SET status = 'running', attempts = attempts + 1, updated_at = now()
WHERE backfill_id = @backfill_id AND range_start = @range_start AND status <> 'done';

-- name: EndBackfillUnit :execrows
-- Done is final: a unit is finished exactly once.
UPDATE backfill_units SET status = @status, last_error_class = sqlc.narg(error_class), updated_at = now()
WHERE backfill_id = @backfill_id AND range_start = @range_start AND status <> 'done';

-- name: FinishBackfill :exec
-- Once no unit is pending or running, the backfill is done, or failed if a unit failed.
UPDATE backfills b SET finished_at = now(),
       status = CASE WHEN EXISTS (SELECT 1 FROM backfill_units u WHERE u.backfill_id = b.id AND u.status = 'failed')
                     THEN 'failed' ELSE 'done' END
WHERE b.id = @id AND b.status = 'running'
  AND NOT EXISTS (SELECT 1 FROM backfill_units u WHERE u.backfill_id = b.id AND u.status IN ('pending', 'running'));

-- name: ReopenBackfillUnits :many
-- Failed units go back to pending; every unfinished unit is returned so its job can be enqueued
-- again (a dedupe key keeps a unit that still has an active job from getting a second one).
UPDATE backfill_units u SET status = CASE WHEN u.status = 'failed' THEN 'pending' ELSE u.status END, updated_at = now()
FROM backfills b
WHERE b.id = u.backfill_id AND b.id = @backfill_id AND b.status IN ('running', 'failed') AND u.status <> 'done'
  AND (sqlc.narg(range_start)::timestamptz IS NULL OR u.range_start = sqlc.narg(range_start))
RETURNING u.range_start, b.connection_id;

-- name: ReopenBackfill :exec
UPDATE backfills SET status = 'running', finished_at = NULL WHERE id = @id AND status IN ('running', 'failed');

-- name: CancelBackfill :one
UPDATE backfills SET status = 'cancelled', finished_at = now()
WHERE id = @id AND status IN ('running', 'failed')
RETURNING connection_id;

-- name: CancelBackfillJobs :exec
-- A running unit finishes its current page and then sees the cancellation.
UPDATE jobs SET status = 'cancelled', finished_at = now()
WHERE kind = 'backfill_unit' AND status = 'queued' AND connection_id = @connection_id
  AND payload->>'backfill_id' = @backfill_id::text;

-- name: ListBackfills :many
SELECT b.*,
       count(*) FILTER (WHERE u.status = 'pending') AS pending,
       count(*) FILTER (WHERE u.status = 'running') AS running,
       count(*) FILTER (WHERE u.status = 'done') AS done,
       count(*) FILTER (WHERE u.status = 'failed') AS failed
FROM backfills b JOIN backfill_units u ON u.backfill_id = b.id
WHERE b.connection_id = @connection_id
GROUP BY b.id
ORDER BY b.created_at DESC, b.id;

-- name: ListBackfillUnits :many
SELECT * FROM backfill_units
WHERE backfill_id = @backfill_id AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
ORDER BY range_start;
