-- Owner read API over canonical rows (J10.2, docs/architecture/api.md#owner-endpoints-apiv1).
-- Every filter is a nullable parameter: NULL means "no filter". Lists are keyset-paginated on
-- (start, id) and return lim rows; after_key NULL starts at the beginning (after_id is then ignored).
-- Active rows only unless with_superseded / with_deleted. The source columns at the end of each
-- list query have the same names everywhere.

-- name: ReadMeasurements :many
SELECT x.id, mc.code AS metric, x.kind, x.start_at, x.end_at, x.tz_offset_min, x.local_date, x.value,
  u.code AS unit, x.source_value, su.code AS source_unit, x.quality_flags, x.group_id, x.context,
  p.code AS provider, x.connection_id, x.device_id, d.device_type, o.origin_key, x.external_id, x.dedupe_key,
  x.raw_payload_id, nv.name AS normalizer_name, nv.version AS normalizer_version, x.ingested_at, x.normalized_at,
  x.superseded_at, COALESCE(x.superseded_by::text, '')::text AS superseded_by, x.deleted_at, x.deleted_by_raw_id
FROM measurements x
JOIN metric_catalog mc ON mc.id = x.metric_id
JOIN units u ON u.id = mc.unit_id
LEFT JOIN units su ON su.id = x.source_unit_id
JOIN providers p ON p.id = x.provider_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id
  AND (sqlc.narg(metrics)::text[] IS NULL OR x.metric_id IN (SELECT id FROM metric_catalog WHERE code = ANY(sqlc.narg(metrics)::text[])))
  AND (sqlc.narg(kinds)::text[] IS NULL OR x.kind = ANY(sqlc.narg(kinds)::text[]))
  AND (sqlc.narg(providers)::text[] IS NULL OR x.provider_id IN (SELECT id FROM providers WHERE code = ANY(sqlc.narg(providers)::text[])))
  AND (sqlc.narg(connections)::uuid[] IS NULL OR x.connection_id = ANY(sqlc.narg(connections)::uuid[]))
  AND (sqlc.narg(devices)::uuid[] IS NULL OR x.device_id = ANY(sqlc.narg(devices)::uuid[]))
  AND (sqlc.narg(origins)::text[] IS NULL OR x.origin_id IN (SELECT id FROM data_origins WHERE user_id = @user_id AND origin_key = ANY(sqlc.narg(origins)::text[])))
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR x.start_at >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR x.start_at < sqlc.narg(end_at)::timestamptz)
  AND (sqlc.narg(start_date)::date IS NULL OR x.local_date >= sqlc.narg(start_date)::date)
  AND (sqlc.narg(end_date)::date IS NULL OR x.local_date <= sqlc.narg(end_date)::date)
  AND (@with_superseded::boolean OR x.superseded_at IS NULL)
  AND (@with_deleted::boolean OR x.deleted_at IS NULL)
  AND (sqlc.narg(after_key)::timestamptz IS NULL OR (x.start_at, x.id) > (sqlc.narg(after_key)::timestamptz, @after_id::bigint))
ORDER BY x.start_at, x.id
LIMIT @lim;

-- name: ReadGroups :many
SELECT x.id, x.kind, x.measured_at, x.tz_offset_min, x.local_date, x.context,
  p.code AS provider, x.connection_id, x.device_id, d.device_type, o.origin_key, x.external_id, x.dedupe_key,
  x.raw_payload_id, nv.name AS normalizer_name, nv.version AS normalizer_version, x.ingested_at, x.normalized_at,
  x.superseded_at, COALESCE(x.superseded_by::text, '')::text AS superseded_by, x.deleted_at, x.deleted_by_raw_id
FROM measurement_groups x
JOIN providers p ON p.id = x.provider_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id
  AND (sqlc.narg(kind)::text IS NULL OR x.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(providers)::text[] IS NULL OR x.provider_id IN (SELECT id FROM providers WHERE code = ANY(sqlc.narg(providers)::text[])))
  AND (sqlc.narg(connections)::uuid[] IS NULL OR x.connection_id = ANY(sqlc.narg(connections)::uuid[]))
  AND (sqlc.narg(devices)::uuid[] IS NULL OR x.device_id = ANY(sqlc.narg(devices)::uuid[]))
  AND (sqlc.narg(origins)::text[] IS NULL OR x.origin_id IN (SELECT id FROM data_origins WHERE user_id = @user_id AND origin_key = ANY(sqlc.narg(origins)::text[])))
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR x.measured_at >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR x.measured_at < sqlc.narg(end_at)::timestamptz)
  AND (sqlc.narg(start_date)::date IS NULL OR x.local_date >= sqlc.narg(start_date)::date)
  AND (sqlc.narg(end_date)::date IS NULL OR x.local_date <= sqlc.narg(end_date)::date)
  AND (@with_superseded::boolean OR x.superseded_at IS NULL)
  AND (@with_deleted::boolean OR x.deleted_at IS NULL)
  AND (sqlc.narg(after_key)::timestamptz IS NULL OR (x.measured_at, x.id) > (sqlc.narg(after_key)::timestamptz, @after_id::bigint))
ORDER BY x.measured_at, x.id
LIMIT @lim;

-- name: ReadGroupComponents :many
-- The components of each group version: the last version of each component that still points at
-- that group row. Deleted components show only on a deleted group.
SELECT m.group_id::bigint AS group_id, m.id, mc.code AS metric, m.value, u.code AS unit, m.source_value,
  su.code AS source_unit, m.quality_flags
FROM measurements m
JOIN measurement_groups g ON g.id = m.group_id
JOIN metric_catalog mc ON mc.id = m.metric_id
JOIN units u ON u.id = mc.unit_id
LEFT JOIN units su ON su.id = m.source_unit_id
WHERE m.group_id = ANY(@group_ids::bigint[])
  AND (m.deleted_at IS NULL OR g.deleted_at IS NOT NULL)
  AND NOT EXISTS (SELECT 1 FROM measurements n WHERE n.id = m.superseded_by AND n.group_id = m.group_id)
ORDER BY m.group_id, mc.code, m.id;

-- name: ReadSleep :many
SELECT x.id, x.start_at, x.end_at, x.tz_offset_min, x.sleep_date, x.is_nap, x.has_stages, x.totals_basis,
  x.asleep_s, x.deep_s, x.light_s, x.rem_s, x.awake_s, x.latency_s,
  p.code AS provider, x.connection_id, x.device_id, d.device_type, o.origin_key, x.external_id, x.dedupe_key,
  x.raw_payload_id, nv.name AS normalizer_name, nv.version AS normalizer_version, x.ingested_at, x.normalized_at,
  x.superseded_at, COALESCE(x.superseded_by::text, '')::text AS superseded_by, x.deleted_at, x.deleted_by_raw_id
FROM sleep_sessions x
JOIN providers p ON p.id = x.provider_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id
  AND (sqlc.narg(id)::uuid IS NULL OR x.id = sqlc.narg(id)::uuid)
  AND (sqlc.narg(providers)::text[] IS NULL OR x.provider_id IN (SELECT id FROM providers WHERE code = ANY(sqlc.narg(providers)::text[])))
  AND (sqlc.narg(connections)::uuid[] IS NULL OR x.connection_id = ANY(sqlc.narg(connections)::uuid[]))
  AND (sqlc.narg(devices)::uuid[] IS NULL OR x.device_id = ANY(sqlc.narg(devices)::uuid[]))
  AND (sqlc.narg(origins)::text[] IS NULL OR x.origin_id IN (SELECT id FROM data_origins WHERE user_id = @user_id AND origin_key = ANY(sqlc.narg(origins)::text[])))
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR x.start_at >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR x.start_at < sqlc.narg(end_at)::timestamptz)
  AND (sqlc.narg(start_date)::date IS NULL OR x.sleep_date >= sqlc.narg(start_date)::date)
  AND (sqlc.narg(end_date)::date IS NULL OR x.sleep_date <= sqlc.narg(end_date)::date)
  AND (@with_superseded::boolean OR x.superseded_at IS NULL)
  AND (@with_deleted::boolean OR x.deleted_at IS NULL)
  AND (sqlc.narg(after_key)::timestamptz IS NULL OR (x.start_at, x.id) > (sqlc.narg(after_key)::timestamptz, @after_id::uuid))
ORDER BY x.start_at, x.id
LIMIT @lim;

-- name: ReadSleepStages :many
SELECT session_id, stage, start_at, end_at FROM sleep_stages
WHERE session_id = ANY(@session_ids::uuid[])
ORDER BY session_id, start_at, id;

-- name: ReadWorkouts :many
SELECT x.id, x.start_at, x.end_at, x.tz_offset_min, x.local_date, x.sport, x.provider_sport, x.distance_m,
  x.energy_kcal, x.avg_hr_bpm, x.max_hr_bpm, x.file_blob_sha256,
  p.code AS provider, x.connection_id, x.device_id, d.device_type, o.origin_key, x.external_id, x.dedupe_key,
  x.raw_payload_id, nv.name AS normalizer_name, nv.version AS normalizer_version, x.ingested_at, x.normalized_at,
  x.superseded_at, COALESCE(x.superseded_by::text, '')::text AS superseded_by, x.deleted_at, x.deleted_by_raw_id
FROM workouts x
JOIN providers p ON p.id = x.provider_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id
  AND (sqlc.narg(id)::uuid IS NULL OR x.id = sqlc.narg(id)::uuid)
  AND (sqlc.narg(providers)::text[] IS NULL OR x.provider_id IN (SELECT id FROM providers WHERE code = ANY(sqlc.narg(providers)::text[])))
  AND (sqlc.narg(connections)::uuid[] IS NULL OR x.connection_id = ANY(sqlc.narg(connections)::uuid[]))
  AND (sqlc.narg(devices)::uuid[] IS NULL OR x.device_id = ANY(sqlc.narg(devices)::uuid[]))
  AND (sqlc.narg(origins)::text[] IS NULL OR x.origin_id IN (SELECT id FROM data_origins WHERE user_id = @user_id AND origin_key = ANY(sqlc.narg(origins)::text[])))
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR x.start_at >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR x.start_at < sqlc.narg(end_at)::timestamptz)
  AND (sqlc.narg(start_date)::date IS NULL OR x.local_date >= sqlc.narg(start_date)::date)
  AND (sqlc.narg(end_date)::date IS NULL OR x.local_date <= sqlc.narg(end_date)::date)
  AND (@with_superseded::boolean OR x.superseded_at IS NULL)
  AND (@with_deleted::boolean OR x.deleted_at IS NULL)
  AND (sqlc.narg(after_key)::timestamptz IS NULL OR (x.start_at, x.id) > (sqlc.narg(after_key)::timestamptz, @after_id::uuid))
ORDER BY x.start_at, x.id
LIMIT @lim;

-- name: ReadWorkoutSegments :many
SELECT workout_id, seq, kind, start_at, end_at, data FROM workout_segments
WHERE workout_id = ANY(@workout_ids::uuid[])
ORDER BY workout_id, seq;

-- name: ReadRawRefs :many
-- Fetch metadata of the given raw payloads (include=provenance); never the body.
SELECT r.id, r.stream, r.external_key, r.version, r.fetched_at, r.batch_id, ib.source_kind
FROM raw_payloads r
JOIN ingest_batches ib ON ib.id = r.batch_id
WHERE r.user_id = @user_id AND r.id = ANY(@ids::bigint[]);

-- name: GetEventFile :one
-- The blob document of one event of the owner, any version; null file_blob_sha256 when it has none.
SELECT code, file_blob_sha256 FROM health_events WHERE user_id = @user_id AND id = @id;

-- name: GetWorkoutRouteFile :one
-- The route of one workout of the owner (docs/adr/0024-watch-data.md#storage-no-new-tables): the
-- active workout_route event naming the workout's external id as context.workout_uuid, else one
-- of the same connection and origin lying inside the workout. Routes are found when read, so
-- arrival order does not matter and the workout row is never rewritten.
SELECT e.file_blob_sha256::bytea AS file_blob_sha256
FROM workouts w
JOIN health_events e ON e.user_id = w.user_id AND e.code = 'workout_route'
  AND e.superseded_at IS NULL AND e.deleted_at IS NULL AND e.file_blob_sha256 IS NOT NULL
  AND e.start_at >= w.start_at - interval '1 day' AND e.start_at < w.end_at + interval '1 day'
WHERE w.user_id = @user_id AND w.id = @id
  AND ((w.external_id IS NOT NULL AND e.context ->> 'workout_uuid' = w.external_id)
    OR (e.context ->> 'workout_uuid' IS NULL AND e.connection_id = w.connection_id
        AND e.origin_id IS NOT DISTINCT FROM w.origin_id AND e.start_at >= w.start_at AND e.start_at < w.end_at))
ORDER BY (e.context ->> 'workout_uuid' IS NULL), e.start_at, e.id
LIMIT 1;
