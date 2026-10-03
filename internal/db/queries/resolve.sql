-- Resolution reads (J09.8, internal/resolve/load.go): active canonical rows with the selector
-- identity rules match on (docs/architecture/resolution.md#selectors-and-validation).

-- name: ResolveMeasurements :many
-- Active rows of the metrics starting from from_at and before to_at. Callers pad the range so
-- intervals crossing into a window and rows of its local dates are included. metric_idx is the
-- 1-based position of the row's code in metrics; the source ids resolve through
-- ResolveSourceIdentities, which keeps a dense series small on the wire.
SELECT x.id, array_position(@metrics::text[], mc.code)::integer AS metric_idx, x.kind, x.start_at, x.end_at,
  x.local_date, x.value, x.quality_flags, COALESCE(x.group_id, 0)::bigint AS group_id, x.provider_id, x.connection_id,
  x.device_id, x.origin_id
FROM measurements x
JOIN metric_catalog mc ON mc.id = x.metric_id
WHERE x.user_id = @user_id
  AND x.metric_id IN (SELECT id FROM metric_catalog WHERE code = ANY(@metrics::text[]))
  AND x.start_at >= @from_at AND x.start_at < @to_at
  AND x.superseded_at IS NULL AND x.deleted_at IS NULL
ORDER BY x.start_at, x.id;

-- name: ResolveSourceIdentities :many
-- The selector identity of (provider, device, origin) id triples; uuid.Nil stands for none.
-- i is the 1-based position of the triple.
SELECT k.i::integer AS i, p.code AS provider,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name,
  (o.relayed_provider_id IS NOT NULL)::boolean AS relayed
FROM (SELECT unnest(@provider_ids::smallint[]) AS provider_id, unnest(@device_ids::uuid[]) AS device_id,
             unnest(@origin_ids::uuid[]) AS origin_id,
             generate_series(1, cardinality(@provider_ids::smallint[])) AS i) AS k
JOIN providers p ON p.id = k.provider_id
LEFT JOIN devices d ON d.id = k.device_id
LEFT JOIN data_origins o ON o.id = k.origin_id;

-- name: ResolveSleepSessions :many
-- Active sleep sessions with sleep_date from from_date through to_date. A night reads its own
-- date and the day before.
SELECT x.id, x.start_at, x.end_at, x.tz_offset_min, x.is_nap, x.has_stages,
  x.asleep_s, x.deep_s, x.light_s, x.rem_s, x.awake_s, x.latency_s,
  p.code AS provider, x.connection_id, x.device_id,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name,
  (o.relayed_provider_id IS NOT NULL)::boolean AS relayed
FROM sleep_sessions x
JOIN providers p ON p.id = x.provider_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id AND x.sleep_date BETWEEN @from_date AND @to_date
  AND x.superseded_at IS NULL AND x.deleted_at IS NULL
ORDER BY x.start_at, x.id;

-- name: ResolveSleepStages :many
SELECT session_id, stage, start_at, end_at FROM sleep_stages
WHERE session_id = ANY(@session_ids::uuid[])
ORDER BY session_id, start_at, id;

-- name: ResolveWorkouts :many
-- Active workouts overlapping from_at to to_at. Callers set starts_from a day before from_at.
SELECT x.id, x.start_at, x.end_at, x.sport, x.distance_m, x.energy_kcal, x.avg_hr_bpm, x.max_hr_bpm,
  p.code AS provider, x.connection_id, x.device_id,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name,
  (o.relayed_provider_id IS NOT NULL)::boolean AS relayed
FROM workouts x
JOIN providers p ON p.id = x.provider_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id
  AND x.start_at >= @starts_from AND x.start_at < @to_at AND x.end_at > @from_at
  AND x.superseded_at IS NULL AND x.deleted_at IS NULL
ORDER BY x.start_at, x.id;

-- name: ResolveWearBuckets :many
-- The wear series of quality.require_wear (E3) as the UTC-aligned 5-minute buckets in which each
-- source has a row of the metric starting from from_at and before to_at. Manual and implausible
-- rows are not wear (not_wear holds those flag bits). A bucket stands for its rows: the wear gate
-- only asks whether a device has a row in a base bucket.
SELECT b.bucket::timestamptz AS bucket, p.code AS provider, b.connection_id, b.device_id,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name,
  (o.relayed_provider_id IS NOT NULL)::boolean AS relayed
FROM (
  SELECT date_bin('5 minutes', x.start_at, TIMESTAMPTZ '2000-01-01 00:00:00+00') AS bucket,
    x.provider_id, x.connection_id, x.device_id, x.origin_id
  FROM measurements x
  WHERE x.user_id = @user_id
    AND x.metric_id = (SELECT id FROM metric_catalog WHERE code = @metric::text)
    AND x.start_at >= @from_at AND x.start_at < @to_at
    AND x.superseded_at IS NULL AND x.deleted_at IS NULL
    AND x.quality_flags & @not_wear::integer = 0
  GROUP BY 1, 2, 3, 4, 5
) b
JOIN providers p ON p.id = b.provider_id
LEFT JOIN devices d ON d.id = b.device_id
LEFT JOIN data_origins o ON o.id = b.origin_id
WHERE p.code <> 'manual'
ORDER BY 1;
