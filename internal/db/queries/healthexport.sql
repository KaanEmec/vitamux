-- Apple Health export importer (J15.8; docs/architecture/apple-health.md#export-importer-fallback).

-- name: AppleExportOverlaps :many
-- The ordinals (@ords) of the probes that match an active row the HealthKit app synced (raw stream
-- healthkit.samples.v1) for the user: same kind and code (metric, sleep stage, sport or event
-- code), origin name, start and end (= start for samples) within a second, since the export
-- writes whole seconds, and, unless the probe value is NaN, the value in the source unit within a
-- relative 1e-4.
WITH p AS (
  SELECT unnest(@ords::integer[]) AS ord, unnest(@kinds::text[]) AS kind, unnest(@codes::text[]) AS code,
    unnest(@starts::timestamptz[]) AS start_at, unnest(@ends::timestamptz[]) AS end_at,
    unnest(@vals::float8[]) AS val, unnest(@origins::text[]) AS origin
)
SELECT p.ord::integer AS ord FROM p
WHERE CASE p.kind
  WHEN 'measurement' THEN EXISTS (
    SELECT 1 FROM measurements m
    JOIN metric_catalog c ON c.id = m.metric_id
    JOIN data_origins o ON o.id = m.origin_id
    JOIN raw_payloads r ON r.id = m.raw_payload_id
    WHERE m.user_id = @user_id AND c.code = p.code
      AND m.start_at > p.start_at - interval '1 second' AND m.start_at < p.start_at + interval '1 second'
      AND abs(extract(epoch FROM coalesce(m.end_at, m.start_at) - p.end_at)) < 1 AND o.name = p.origin
      AND r.stream = 'healthkit.samples.v1' AND m.superseded_at IS NULL AND m.deleted_at IS NULL
      AND (p.val = 'NaN'::float8 OR abs(coalesce(m.source_value, m.value) - p.val) <= 1e-4 * greatest(abs(p.val), 1e-6)))
  WHEN 'sleep' THEN EXISTS (
    SELECT 1 FROM sleep_sessions s
    JOIN sleep_stages st ON st.session_id = s.id
    JOIN data_origins o ON o.id = s.origin_id
    JOIN raw_payloads r ON r.id = s.raw_payload_id
    WHERE s.user_id = @user_id AND s.sleep_date BETWEEN (p.start_at AT TIME ZONE 'UTC')::date - 2 AND (p.end_at AT TIME ZONE 'UTC')::date + 2
      AND st.stage = p.code AND abs(extract(epoch FROM st.start_at - p.start_at)) < 1
      AND abs(extract(epoch FROM st.end_at - p.end_at)) < 1 AND o.name = p.origin
      AND r.stream = 'healthkit.samples.v1' AND s.superseded_at IS NULL AND s.deleted_at IS NULL)
  WHEN 'workout' THEN EXISTS (
    SELECT 1 FROM workouts w
    JOIN data_origins o ON o.id = w.origin_id
    JOIN raw_payloads r ON r.id = w.raw_payload_id
    WHERE w.user_id = @user_id AND w.sport = p.code
      AND w.start_at > p.start_at - interval '1 second' AND w.start_at < p.start_at + interval '1 second'
      AND abs(extract(epoch FROM w.end_at - p.end_at)) < 1
      AND o.name = p.origin AND r.stream = 'healthkit.samples.v1' AND w.superseded_at IS NULL AND w.deleted_at IS NULL)
  WHEN 'event' THEN EXISTS (
    SELECT 1 FROM health_events e
    JOIN data_origins o ON o.id = e.origin_id
    JOIN raw_payloads r ON r.id = e.raw_payload_id
    WHERE e.user_id = @user_id AND e.code = p.code
      AND e.start_at > p.start_at - interval '1 second' AND e.start_at < p.start_at + interval '1 second'
      AND abs(extract(epoch FROM coalesce(e.end_at, e.start_at) - p.end_at)) < 1 AND o.name = p.origin
      AND r.stream = 'healthkit.samples.v1' AND e.superseded_at IS NULL AND e.deleted_at IS NULL)
  ELSE false
END
ORDER BY p.ord;

-- name: InsertImportRun :exec
INSERT INTO import_runs (id, user_id, connection_id, source, status, stats, finished_at)
VALUES (@id, @user_id, @connection_id, @source, 'done', @stats, now());
