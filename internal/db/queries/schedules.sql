-- Schedules (J06.3); see docs/architecture/reliability.md#scheduler.

-- name: InsertSchedule :one
INSERT INTO schedules (id, connection_id, stream, mode, run_interval, lookback, next_run_at)
VALUES (@id, @connection_id, @stream, @mode, @run_interval, @lookback, now() + @run_interval)
ON CONFLICT (connection_id, stream, mode) DO NOTHING
RETURNING *;

-- name: GetSchedule :one
SELECT * FROM schedules WHERE id = @id;

-- name: GetScheduleByStream :one
SELECT * FROM schedules WHERE connection_id = @connection_id AND stream = @stream AND mode = @mode;

-- name: UpdateSchedule :one
-- A changed interval may only bring the next run closer, never push it out by a full old interval.
UPDATE schedules SET run_interval = @run_interval, lookback = @lookback, enabled = @enabled,
       next_run_at = CASE WHEN run_interval = @run_interval THEN next_run_at
                          ELSE least(next_run_at, now() + @run_interval) END
WHERE id = @id
RETURNING *;

-- name: ListSchedules :many
SELECT * FROM schedules
WHERE sqlc.narg(connection_id)::uuid IS NULL OR connection_id = sqlc.narg(connection_id)
ORDER BY connection_id, stream, mode;

-- name: LockDueSchedules :many
-- Schedules of paused, needs_reauth, error or disabled connections wait until it is active again.
SELECT s.* FROM schedules s JOIN connections c ON c.id = s.connection_id
WHERE s.enabled AND s.next_run_at <= @now AND c.status IN ('active', 'degraded')
ORDER BY s.next_run_at
LIMIT @max_rows
FOR UPDATE OF s SKIP LOCKED;

-- name: AdvanceSchedule :exec
UPDATE schedules SET next_run_at = @next_run_at WHERE id = @id;
