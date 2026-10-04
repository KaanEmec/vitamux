-- Inventory, events and source series (J21.5, J21.6; docs/architecture/api.md). Metric counts come
-- from the hourly aggregates plus daily values, so a request never scans the measurements of a
-- whole history; the other record tables are small and read directly.

-- name: InventoryMetricBounds :many
-- The first and newest active row of every metric that has one: two index probes per catalogue code.
SELECT mc.code AS metric, f.start_at AS first_at, f.local_date AS first_date,
  l.start_at AS last_at, l.local_date AS last_date, l.value AS latest_value
FROM metric_catalog mc
CROSS JOIN LATERAL (
  SELECT x.start_at, x.local_date FROM measurements x
  WHERE x.user_id = @user_id AND x.metric_id = mc.id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  ORDER BY x.start_at LIMIT 1) f
CROSS JOIN LATERAL (
  SELECT x.start_at, x.local_date, x.value FROM measurements x
  WHERE x.user_id = @user_id AND x.metric_id = mc.id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  ORDER BY x.start_at DESC, x.id DESC LIMIT 1) l
ORDER BY mc.code;

-- name: InventoryMetricSources :many
-- Per metric and source: rows in the hourly aggregates (an interval once per hour it touches)
-- plus active daily values, with the metric's local dates that have any.
WITH src AS MATERIALIZED (
  SELECT a.metric_id, a.connection_id, a.device_id, a.origin_id, a.local_date, a.samples::bigint AS n
  FROM source_hourly_aggregates a WHERE a.user_id = @user_id
  UNION ALL
  SELECT x.metric_id, x.connection_id, x.device_id, x.origin_id, x.local_date, 1::bigint
  FROM measurements x
  WHERE x.user_id = @user_id AND x.kind = 'daily_value' AND x.superseded_at IS NULL AND x.deleted_at IS NULL
), days AS (
  SELECT d.metric_id, count(*)::integer AS days FROM (SELECT DISTINCT metric_id, local_date FROM src) d GROUP BY d.metric_id
)
SELECT mc.code AS metric, dy.days, s.n::bigint AS count, p.code AS provider, s.device_id,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name
FROM (SELECT metric_id, connection_id, device_id, origin_id, sum(n) AS n FROM src GROUP BY 1, 2, 3, 4) s
JOIN days dy ON dy.metric_id = s.metric_id
JOIN metric_catalog mc ON mc.id = s.metric_id
JOIN connections c ON c.id = s.connection_id
JOIN providers p ON p.id = c.provider_id
LEFT JOIN devices d ON d.id = s.device_id
LEFT JOIN data_origins o ON o.id = s.origin_id
ORDER BY mc.code, p.code, s.connection_id;

-- name: InventoryRecords :many
-- Per kind (group, event, sleep, workouts), code and source: active records and their bounds,
-- with the code's local dates and its newest record's value, level, text and group id.
WITH r AS MATERIALIZED (
  SELECT 'event'::text AS kind, x.code, x.start_at AS at, x.local_date, x.connection_id, x.device_id, x.origin_id,
    x.value, x.level, NULL::text AS text, NULL::bigint AS group_id
  FROM health_events x WHERE x.user_id = @user_id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT 'group', x.kind, x.measured_at, x.local_date, x.connection_id, x.device_id, x.origin_id, NULL, NULL, NULL, x.id
  FROM measurement_groups x WHERE x.user_id = @user_id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT 'sleep', 'sleep', x.start_at, x.sleep_date, x.connection_id, x.device_id, x.origin_id, x.asleep_s::float8, NULL, NULL, NULL
  FROM sleep_sessions x WHERE x.user_id = @user_id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  UNION ALL
  SELECT 'workouts', 'workouts', x.start_at, x.local_date, x.connection_id, x.device_id, x.origin_id, NULL, NULL, x.sport, NULL
  FROM workouts x WHERE x.user_id = @user_id AND x.superseded_at IS NULL AND x.deleted_at IS NULL
), days AS (
  SELECT kind, code, count(DISTINCT local_date)::integer AS days FROM r GROUP BY kind, code
), newest AS (
  SELECT DISTINCT ON (kind, code) kind, code, value, level, text, group_id FROM r ORDER BY kind, code, at DESC
)
SELECT s.kind, s.code, dy.days, s.n AS count, s.first_at, s.last_at, s.first_date, s.last_date,
  l.value AS latest_value, l.level AS latest_level, l.text AS latest_text, l.group_id AS latest_group_id,
  p.code AS provider, s.device_id,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name
FROM (
  SELECT kind, code, connection_id, device_id, origin_id, count(*)::bigint AS n,
    min(at)::timestamptz AS first_at, max(at)::timestamptz AS last_at,
    min(local_date)::date AS first_date, max(local_date)::date AS last_date
  FROM r GROUP BY 1, 2, 3, 4, 5
) s
JOIN days dy ON dy.kind = s.kind AND dy.code = s.code
LEFT JOIN newest l ON l.kind = s.kind AND l.code = s.code
JOIN connections c ON c.id = s.connection_id
JOIN providers p ON p.id = c.provider_id
LEFT JOIN devices d ON d.id = s.device_id
LEFT JOIN data_origins o ON o.id = s.origin_id
ORDER BY s.kind, s.code, p.code, s.connection_id;

-- name: InventoryAnalytes :many
-- Confirmed lab results per catalogue analyte, with the newest one: its canonical value and unit,
-- else its printed number and unit, and its printed text.
WITH r AS (
  SELECT x.analyte_id, x.id, x.collected_at, x.collected_date, COALESCE(x.canonical_value, x.value_numeric)::float8 AS value,
    (CASE WHEN x.canonical_value IS NOT NULL THEN x.canonical_unit ELSE x.unit_text END)::text AS unit, x.value_text
  FROM lab_results x WHERE x.user_id = @user_id AND x.analyte_id IS NOT NULL
), s AS (
  SELECT analyte_id, count(*)::bigint AS n, count(DISTINCT collected_date)::integer AS days,
    min(collected_date)::date AS first_date, max(collected_date)::date AS last_date
  FROM r GROUP BY analyte_id
), t AS (
  SELECT analyte_id, min(collected_at)::timestamptz AS first_at, max(collected_at)::timestamptz AS last_at
  FROM r WHERE collected_at IS NOT NULL GROUP BY analyte_id
), newest AS (
  SELECT DISTINCT ON (analyte_id) analyte_id, collected_at, collected_date, value, unit, value_text
  FROM r ORDER BY analyte_id, collected_date DESC, collected_at DESC NULLS LAST, id DESC
)
SELECT a.code, a.name, a.canonical_unit, s.n AS count, s.days, s.first_date, s.last_date, t.first_at, t.last_at,
  l.collected_at AS latest_at, l.collected_date AS latest_date, l.value AS latest_value, l.unit AS latest_unit,
  l.value_text AS latest_text
FROM s
JOIN analytes a ON a.id = s.analyte_id
LEFT JOIN t ON t.analyte_id = s.analyte_id
LEFT JOIN newest l ON l.analyte_id = s.analyte_id
ORDER BY a.code;

-- name: AggregatesPending :one
-- Whether dirty marks (of one metric and range, when given) still wait for the rebuild job.
SELECT EXISTS (
  SELECT 1 FROM resolution_dirty r
  WHERE r.user_id = @user_id
    AND (sqlc.narg(metric)::text IS NULL OR r.metric_id = (SELECT id FROM metric_catalog WHERE code = sqlc.narg(metric)::text))
    AND (sqlc.narg(from_date)::date IS NULL OR r.local_date >= sqlc.narg(from_date)::date)
    AND (sqlc.narg(to_date)::date IS NULL OR r.local_date <= sqlc.narg(to_date)::date))::boolean;

-- name: ReadEvents :many
SELECT x.id, x.code, x.start_at, x.end_at, x.tz_offset_min, x.local_date, x.value, x.level, x.context, x.quality_flags,
  p.code AS provider, x.connection_id, x.device_id, d.device_type, o.origin_key, x.external_id, x.dedupe_key,
  x.raw_payload_id, nv.name AS normalizer_name, nv.version AS normalizer_version, x.ingested_at, x.normalized_at,
  x.superseded_at, COALESCE(x.superseded_by::text, '')::text AS superseded_by, x.deleted_at, x.deleted_by_raw_id
FROM health_events x
JOIN providers p ON p.id = x.provider_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id
  AND (sqlc.narg(codes)::text[] IS NULL OR x.code = ANY(sqlc.narg(codes)::text[]))
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

-- name: SourceSeriesAggregates :many
-- One metric's hourly aggregates from from_at to to_at per source with its identity, by local
-- hour or (by_day) summed per local date, whose hour_start is then its first hour. Ordered by
-- source, then time.
SELECT min(a.hour_start)::timestamptz AS hour_start, a.local_date,
  a.connection_id, a.device_id, a.origin_id, p.code AS provider,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name,
  (o.relayed_provider_id IS NOT NULL)::boolean AS relayed,
  sum(a.samples)::integer AS samples, sum(a.buckets)::integer AS buckets, sum(a.bucket_mean_sum)::float8 AS bucket_mean_sum,
  min(a.min_value)::float8 AS min_value, max(a.max_value)::float8 AS max_value, sum(a.interval_sum)::float8 AS interval_sum
FROM source_hourly_aggregates a
JOIN connections c ON c.id = a.connection_id
JOIN providers p ON p.id = c.provider_id
LEFT JOIN devices d ON d.id = a.device_id
LEFT JOIN data_origins o ON o.id = a.origin_id
WHERE a.user_id = @user_id AND a.metric_id = (SELECT id FROM metric_catalog WHERE code = @metric::text)
  AND a.hour_start >= @from_at AND a.hour_start < @to_at
GROUP BY CASE WHEN @by_day::boolean THEN NULL ELSE a.hour_start END, a.local_date, a.connection_id, a.device_id, a.origin_id, p.code, d.device_type, d.model, o.origin_key, o.name, o.relayed_provider_id
ORDER BY a.connection_id, a.device_id NULLS FIRST, a.origin_id NULLS FIRST, 1;

-- name: SourceDailyValues :many
-- One metric's active daily values on the local dates from_date through to_date, the newest per
-- source and date, with the source identity.
SELECT DISTINCT ON (x.connection_id, x.device_id, x.origin_id, x.local_date)
  x.local_date, x.value, x.connection_id, x.device_id, x.origin_id, p.code AS provider,
  COALESCE(d.device_type, '')::text AS device_type, COALESCE(d.model, '')::text AS device_model,
  COALESCE(o.origin_key, '')::text AS origin_key, COALESCE(o.name, '')::text AS origin_name,
  (o.relayed_provider_id IS NOT NULL)::boolean AS relayed
FROM measurements x
JOIN providers p ON p.id = x.provider_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
WHERE x.user_id = @user_id AND x.metric_id = (SELECT id FROM metric_catalog WHERE code = @metric::text)
  AND x.kind = 'daily_value' AND x.superseded_at IS NULL AND x.deleted_at IS NULL
  AND x.local_date BETWEEN @from_date AND @to_date
ORDER BY x.connection_id, x.device_id, x.origin_id, x.local_date, x.start_at DESC, x.id DESC;
