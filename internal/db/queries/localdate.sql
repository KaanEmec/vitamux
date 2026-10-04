-- Timezone periods and local-date recomputation (J07.2).

-- name: ListTimezonePeriods :many
SELECT id, tz, valid_from FROM timezone_periods WHERE user_id = @user_id ORDER BY valid_from;

-- name: InsertTimezonePeriod :exec
INSERT INTO timezone_periods (id, user_id, tz, valid_from) VALUES (@id, @user_id, @tz, @valid_from);

-- name: UpdateTimezonePeriod :execrows
UPDATE timezone_periods SET tz = @tz, valid_from = @valid_from WHERE id = @id AND user_id = @user_id;

-- name: DeleteTimezonePeriod :execrows
DELETE FROM timezone_periods WHERE id = @id AND user_id = @user_id;

-- name: ListMetricIDs :many
SELECT id FROM metric_catalog ORDER BY id;

-- Rows whose local date derives from timezone_periods (no stored offset), one keyset page at a time.
-- Only active rows: superseded and deleted rows are history and keep the date they were written with.

-- name: ListMeasurementsForLocalDate :many
SELECT id, start_at AS at, local_date FROM measurements
WHERE user_id = @user_id AND metric_id = @metric_id
  AND tz_offset_min IS NULL AND superseded_at IS NULL AND deleted_at IS NULL
  AND start_at >= @from_at AND start_at < @until_at
  AND (start_at, id) > (@after_at::timestamptz, @after_id::bigint)
ORDER BY start_at, id
LIMIT @batch;

-- name: ListMeasurementGroupsForLocalDate :many
SELECT id, measured_at AS at, local_date FROM measurement_groups
WHERE user_id = @user_id
  AND tz_offset_min IS NULL AND superseded_at IS NULL AND deleted_at IS NULL
  AND measured_at >= @from_at AND measured_at < @until_at
  AND (measured_at, id) > (@after_at::timestamptz, @after_id::bigint)
ORDER BY measured_at, id
LIMIT @batch;

-- name: ListWorkoutsForLocalDate :many
SELECT id, start_at AS at, local_date FROM workouts
WHERE user_id = @user_id
  AND tz_offset_min IS NULL AND superseded_at IS NULL AND deleted_at IS NULL
  AND start_at >= @from_at AND start_at < @until_at
  AND (start_at, id) > (@after_at::timestamptz, @after_id::uuid)
ORDER BY start_at, id
LIMIT @batch;

-- The local date of a sleep session is the date it ended (sleep_date).
-- name: ListSleepSessionsForLocalDate :many
SELECT id, end_at AS at, sleep_date AS local_date FROM sleep_sessions
WHERE user_id = @user_id
  AND tz_offset_min IS NULL AND superseded_at IS NULL AND deleted_at IS NULL
  AND end_at >= @from_at AND end_at < @until_at
  AND (end_at, id) > (@after_at::timestamptz, @after_id::uuid)
ORDER BY end_at, id
LIMIT @batch;

-- name: ListEventsForLocalDate :many
SELECT id, start_at AS at, local_date FROM health_events
WHERE user_id = @user_id
  AND tz_offset_min IS NULL AND superseded_at IS NULL AND deleted_at IS NULL
  AND start_at >= @from_at AND start_at < @until_at
  AND (start_at, id) > (@after_at::timestamptz, @after_id::uuid)
ORDER BY start_at, id
LIMIT @batch;

-- name: SetMeasurementLocalDates :exec
UPDATE measurements m SET local_date = v.d
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@dates::date[]) AS d) AS v WHERE m.id = v.id;

-- name: SetMeasurementGroupLocalDates :exec
UPDATE measurement_groups g SET local_date = v.d
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@dates::date[]) AS d) AS v WHERE g.id = v.id;

-- name: SetWorkoutLocalDates :exec
UPDATE workouts w SET local_date = v.d
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@dates::date[]) AS d) AS v WHERE w.id = v.id;

-- name: SetSleepSessionDates :exec
UPDATE sleep_sessions s SET sleep_date = v.d
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@dates::date[]) AS d) AS v WHERE s.id = v.id;

-- name: SetEventLocalDates :exec
UPDATE health_events e SET local_date = v.d
FROM (SELECT unnest(@ids::uuid[]) AS id, unnest(@dates::date[]) AS d) AS v WHERE e.id = v.id;

-- name: MarkLocalDatesDirty :exec
INSERT INTO resolution_dirty (user_id, metric_id, local_date)
SELECT @user_id, @metric_id, d FROM unnest(@dates::date[]) AS d
ON CONFLICT DO NOTHING;
