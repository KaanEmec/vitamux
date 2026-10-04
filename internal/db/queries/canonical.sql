-- Normalizer versions and the canonical writer (J07.3, J07.4); see docs/adr/0016-canonical-writer.md.
-- A change never updates a row in place: supersede the old row, insert the new one, then link them
-- (superseded_by can only point at a row that exists, and the partial unique index on dedupe_key
-- admits one non-superseded row at a time).

-- name: RegisterNormalizerVersion :one
WITH ins AS (
  INSERT INTO normalizer_versions (name, version, git_sha) VALUES (@name, @version, @git_sha)
  ON CONFLICT DO NOTHING
  RETURNING id
)
SELECT id FROM ins
UNION ALL
SELECT id FROM normalizer_versions WHERE name = @name AND version = @version AND git_sha = @git_sha
LIMIT 1;

-- name: GetWriteConnection :one
SELECT c.user_id, c.provider_id, p.code AS provider, c.account_key
FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.id = @id;

-- name: ListMetricCodes :many
SELECT id, code FROM metric_catalog;

-- name: ListUnitCodes :many
SELECT id, code FROM units;

-- Descriptive fields only fill in or change; a field the source stops sending keeps its value.
-- name: UpsertDevice :one
-- Returns the device records of the fingerprint are written to: the device, or the one the owner
-- merged it into. A type the owner set is kept.
WITH ins AS (
  INSERT INTO devices AS d (id, user_id, provider_id, fingerprint, device_type, manufacturer, model,
                            hardware_version, software_version)
  VALUES (@id, @user_id, @provider_id, @fingerprint, sqlc.narg(device_type), sqlc.narg(manufacturer),
          sqlc.narg(model), sqlc.narg(hardware_version), sqlc.narg(software_version))
  ON CONFLICT (user_id, provider_id, fingerprint) DO UPDATE SET
    device_type      = CASE WHEN d.device_type_by_owner THEN d.device_type ELSE COALESCE(EXCLUDED.device_type, d.device_type) END,
    manufacturer     = COALESCE(EXCLUDED.manufacturer, d.manufacturer),
    model            = COALESCE(EXCLUDED.model, d.model),
    hardware_version = COALESCE(EXCLUDED.hardware_version, d.hardware_version),
    software_version = COALESCE(EXCLUDED.software_version, d.software_version)
  WHERE (d.device_type, d.manufacturer, d.model, d.hardware_version, d.software_version) IS DISTINCT FROM
        (CASE WHEN d.device_type_by_owner THEN d.device_type ELSE COALESCE(EXCLUDED.device_type, d.device_type) END,
         COALESCE(EXCLUDED.manufacturer, d.manufacturer),
         COALESCE(EXCLUDED.model, d.model), COALESCE(EXCLUDED.hardware_version, d.hardware_version),
         COALESCE(EXCLUDED.software_version, d.software_version))
  RETURNING COALESCE(d.merged_into, d.id) AS id
)
SELECT id FROM ins
UNION ALL
SELECT COALESCE(merged_into, id) FROM devices WHERE user_id = @user_id AND provider_id = @provider_id AND fingerprint = @fingerprint
LIMIT 1;

-- A new origin takes its relay target from known_relay_origins; later edits to the origin win.
-- name: UpsertOrigin :one
WITH ins AS (
  INSERT INTO data_origins AS o (id, user_id, provider_id, origin_key, name, is_native, relayed_provider_id)
  SELECT @id::uuid, @user_id::uuid, @provider_id::smallint, @origin_key::text, sqlc.narg(name)::text, @is_native::boolean,
         (SELECT k.relayed_provider_id FROM known_relay_origins k
          WHERE k.provider_id = @provider_id::smallint AND @origin_key::text LIKE k.origin_pattern
          ORDER BY k.id LIMIT 1)
  ON CONFLICT (user_id, provider_id, origin_key) DO UPDATE SET name = EXCLUDED.name
  WHERE EXCLUDED.name IS NOT NULL AND o.name IS DISTINCT FROM EXCLUDED.name
  RETURNING o.id, o.relayed_provider_id
)
SELECT id, relayed_provider_id FROM ins
UNION ALL
SELECT id, relayed_provider_id FROM data_origins
WHERE user_id = @user_id::uuid AND provider_id = @provider_id::smallint AND origin_key = @origin_key::text
LIMIT 1;

-- name: MarkDirty :exec
INSERT INTO resolution_dirty (user_id, metric_id, local_date)
SELECT @user_id::uuid, unnest(@metric_ids::smallint[]), unnest(@dates::date[])
ON CONFLICT (user_id, metric_id, local_date) DO UPDATE SET marked_at = EXCLUDED.marked_at;

-- name: ListActiveMeasurements :many
SELECT id, dedupe_key, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value, source_value,
       source_unit_id, device_id, origin_id, group_id, external_id, quality_flags, normalizer_version_id, deleted_at
FROM measurements
WHERE dedupe_key = ANY(@keys::bytea[]) AND superseded_at IS NULL;

-- Rows arrive as a JSON array so nullable columns need no parallel arrays.
-- name: InsertMeasurements :many
INSERT INTO measurements (user_id, metric_id, kind, start_at, end_at, tz_offset_min, local_date, value,
                          source_value, source_unit_id, provider_id, connection_id, device_id, origin_id, group_id,
                          external_id, dedupe_key, quality_flags, raw_payload_id, normalizer_version_id)
SELECT @user_id::uuid, r.metric_id, r.kind, r.start_at, r.end_at, r.tz_offset_min, r.local_date, r.value,
       r.source_value, r.source_unit_id, @provider_id::smallint, @connection_id::uuid, r.device_id, r.origin_id,
       r.group_id, r.external_id, decode(r.dedupe_key, 'hex'), r.quality_flags, @raw_payload_id::bigint,
       @normalizer_version_id::integer
FROM jsonb_to_recordset(@rows::jsonb) AS r (
  metric_id smallint, kind text, start_at timestamptz, end_at timestamptz, tz_offset_min smallint, local_date date,
  value double precision, source_value double precision, source_unit_id smallint, device_id uuid, origin_id uuid,
  group_id bigint, external_id text, dedupe_key text, quality_flags integer)
RETURNING id, dedupe_key;

-- name: TouchMeasurements :exec
UPDATE measurements SET normalizer_version_id = @normalizer_version_id, normalized_at = now()
WHERE id = ANY(@ids::bigint[]);

-- name: SupersedeMeasurements :exec
UPDATE measurements SET superseded_at = now() WHERE id = ANY(@ids::bigint[]);

-- name: LinkMeasurementSuccessors :exec
UPDATE measurements m SET superseded_by = v.new_id
FROM (SELECT unnest(@old_ids::bigint[]) AS old_id, unnest(@new_ids::bigint[]) AS new_id) AS v
WHERE m.id = v.old_id;

-- name: DeleteMeasurementsByKey :many
UPDATE measurements SET deleted_at = now(), deleted_by_raw_id = @raw_payload_id
WHERE dedupe_key = ANY(@keys::bytea[]) AND superseded_at IS NULL AND deleted_at IS NULL
RETURNING metric_id, local_date;

-- name: DeleteGroupComponents :many
UPDATE measurements SET deleted_at = now(), deleted_by_raw_id = @raw_payload_id
WHERE group_id = ANY(@group_ids::bigint[]) AND superseded_at IS NULL AND deleted_at IS NULL
RETURNING metric_id, local_date;

-- name: GetActiveGroup :one
SELECT id, kind, measured_at, tz_offset_min, local_date, context, device_id, origin_id, external_id,
       normalizer_version_id, deleted_at
FROM measurement_groups WHERE dedupe_key = @dedupe_key AND superseded_at IS NULL;

-- name: InsertGroup :one
INSERT INTO measurement_groups (user_id, kind, measured_at, tz_offset_min, local_date, context, provider_id,
                                connection_id, device_id, origin_id, external_id, dedupe_key, raw_payload_id,
                                normalizer_version_id)
VALUES (@user_id, @kind, @measured_at, @tz_offset_min, @local_date, @context, @provider_id, @connection_id,
        @device_id, @origin_id, @external_id, @dedupe_key, @raw_payload_id, @normalizer_version_id)
RETURNING id;

-- name: TouchGroup :exec
UPDATE measurement_groups SET normalizer_version_id = @normalizer_version_id, normalized_at = now() WHERE id = @id;

-- name: SupersedeGroup :exec
UPDATE measurement_groups SET superseded_at = now() WHERE id = @id;

-- name: LinkGroupSuccessor :exec
UPDATE measurement_groups SET superseded_by = @new_id WHERE id = @id;

-- name: DeleteGroupsByKey :many
UPDATE measurement_groups SET deleted_at = now(), deleted_by_raw_id = @raw_payload_id
WHERE dedupe_key = ANY(@keys::bytea[]) AND superseded_at IS NULL AND deleted_at IS NULL
RETURNING id;

-- name: GetActiveSleep :one
SELECT id, start_at, end_at, tz_offset_min, sleep_date, is_nap, has_stages, totals_basis, asleep_s, deep_s,
       light_s, rem_s, awake_s, latency_s, device_id, origin_id, external_id, normalizer_version_id, deleted_at
FROM sleep_sessions WHERE dedupe_key = @dedupe_key AND superseded_at IS NULL;

-- name: ListSleepStages :many
SELECT stage, start_at, end_at FROM sleep_stages WHERE session_id = @session_id ORDER BY start_at, id;

-- name: InsertSleep :exec
INSERT INTO sleep_sessions (id, user_id, start_at, end_at, tz_offset_min, sleep_date, is_nap, has_stages,
                            totals_basis, asleep_s, deep_s, light_s, rem_s, awake_s, latency_s, provider_id,
                            connection_id, device_id, origin_id, external_id, dedupe_key, raw_payload_id,
                            normalizer_version_id)
VALUES (@id, @user_id, @start_at, @end_at, @tz_offset_min, @sleep_date, @is_nap, @has_stages, @totals_basis,
        @asleep_s, @deep_s, @light_s, @rem_s, @awake_s, @latency_s, @provider_id, @connection_id, @device_id,
        @origin_id, @external_id, @dedupe_key, @raw_payload_id, @normalizer_version_id);

-- name: InsertSleepStages :exec
INSERT INTO sleep_stages (session_id, stage, start_at, end_at)
SELECT @session_id::uuid, unnest(@stages::text[]), unnest(@starts::timestamptz[]), unnest(@ends::timestamptz[]);

-- name: TouchSleep :exec
UPDATE sleep_sessions SET normalizer_version_id = @normalizer_version_id, normalized_at = now() WHERE id = @id;

-- name: SupersedeSleep :exec
UPDATE sleep_sessions SET superseded_at = now() WHERE id = @id;

-- name: LinkSleepSuccessor :exec
UPDATE sleep_sessions SET superseded_by = @new_id WHERE id = @id;

-- name: DeleteSleepByKey :many
UPDATE sleep_sessions SET deleted_at = now(), deleted_by_raw_id = @raw_payload_id
WHERE dedupe_key = ANY(@keys::bytea[]) AND superseded_at IS NULL AND deleted_at IS NULL
RETURNING sleep_date;

-- name: GetActiveWorkout :one
SELECT id, start_at, end_at, tz_offset_min, local_date, sport, provider_sport, distance_m, energy_kcal,
       avg_hr_bpm, max_hr_bpm, file_blob_sha256, device_id, origin_id, external_id, normalizer_version_id, deleted_at
FROM workouts WHERE dedupe_key = @dedupe_key AND superseded_at IS NULL;

-- name: ListWorkoutSegments :many
SELECT seq, kind, start_at, end_at, data FROM workout_segments WHERE workout_id = @workout_id ORDER BY seq;

-- name: InsertWorkout :exec
INSERT INTO workouts (id, user_id, start_at, end_at, tz_offset_min, local_date, sport, provider_sport, distance_m,
                      energy_kcal, avg_hr_bpm, max_hr_bpm, file_blob_sha256, provider_id, connection_id, device_id,
                      origin_id, external_id, dedupe_key, raw_payload_id, normalizer_version_id)
VALUES (@id, @user_id, @start_at, @end_at, @tz_offset_min, @local_date, @sport, @provider_sport, @distance_m,
        @energy_kcal, @avg_hr_bpm, @max_hr_bpm, @file_blob_sha256, @provider_id, @connection_id, @device_id,
        @origin_id, @external_id, @dedupe_key, @raw_payload_id, @normalizer_version_id);

-- name: InsertWorkoutSegments :exec
INSERT INTO workout_segments (workout_id, seq, kind, start_at, end_at, data)
SELECT @workout_id::uuid, r.seq, r.kind, r.start_at, r.end_at, COALESCE(r.data, '{}')
FROM jsonb_to_recordset(@rows::jsonb) AS r (seq integer, kind text, start_at timestamptz, end_at timestamptz, data jsonb);

-- name: TouchWorkout :exec
UPDATE workouts SET normalizer_version_id = @normalizer_version_id, normalized_at = now() WHERE id = @id;

-- name: SupersedeWorkout :exec
UPDATE workouts SET superseded_at = now() WHERE id = @id;

-- name: LinkWorkoutSuccessor :exec
UPDATE workouts SET superseded_by = @new_id WHERE id = @id;

-- name: DeleteWorkoutsByKey :execrows
UPDATE workouts SET deleted_at = now(), deleted_by_raw_id = @raw_payload_id
WHERE dedupe_key = ANY(@keys::bytea[]) AND superseded_at IS NULL AND deleted_at IS NULL;

-- name: GetActiveEvent :one
SELECT id, code, start_at, end_at, tz_offset_min, local_date, value, level, context, quality_flags, device_id,
       origin_id, external_id, normalizer_version_id, deleted_at
FROM health_events WHERE dedupe_key = @dedupe_key AND superseded_at IS NULL;

-- name: InsertEvent :exec
INSERT INTO health_events (id, user_id, code, start_at, end_at, tz_offset_min, local_date, value, level, context,
                           quality_flags, provider_id, connection_id, device_id, origin_id, external_id, dedupe_key,
                           raw_payload_id, normalizer_version_id)
VALUES (@id, @user_id, @code, @start_at, @end_at, @tz_offset_min, @local_date, @value, @level, @context,
        @quality_flags, @provider_id, @connection_id, @device_id, @origin_id, @external_id, @dedupe_key,
        @raw_payload_id, @normalizer_version_id);

-- name: TouchEvent :exec
UPDATE health_events SET normalizer_version_id = @normalizer_version_id, normalized_at = now() WHERE id = @id;

-- name: SupersedeEvent :exec
UPDATE health_events SET superseded_at = now() WHERE id = @id;

-- name: LinkEventSuccessor :exec
UPDATE health_events SET superseded_by = @new_id WHERE id = @id;

-- name: DeleteEventsByKey :execrows
UPDATE health_events SET deleted_at = now(), deleted_by_raw_id = @raw_payload_id
WHERE dedupe_key = ANY(@keys::bytea[]) AND superseded_at IS NULL AND deleted_at IS NULL;
