-- Provenance traces (J07.6): one query per canonical table, all returning the same columns so the
-- generated row types convert into each other. Raw payload bodies are never selected, only metadata.

-- name: TraceMeasurementChain :many
-- Every version of one measurement (the given row, its predecessors and its successors along
-- superseded_by), oldest first, each with its provenance. depth is 0 for the given row.
WITH RECURSIVE
back(id, depth) AS (
  SELECT t.id, 0 FROM measurements t WHERE t.id = @id
  UNION ALL
  SELECT t.id, b.depth - 1 FROM measurements t JOIN back b ON t.superseded_by = b.id
),
fwd(id, depth) AS (
  SELECT t.id, 0 FROM measurements t WHERE t.id = @id
  UNION ALL
  SELECT t.superseded_by, f.depth + 1 FROM measurements t JOIN fwd f ON t.id = f.id WHERE t.superseded_by IS NOT NULL
),
chain AS (SELECT id, depth FROM back UNION SELECT id, depth FROM fwd)
SELECT
  x.id::text AS id,
  COALESCE(x.superseded_by::text, '')::text AS superseded_by,
  c.depth::integer AS depth,
  (to_jsonb(x) - 'dedupe_key' || jsonb_build_object('dedupe_key', encode(x.dedupe_key, 'hex'), 'device', d.fingerprint, 'origin', o.origin_key, 'metric', mc.code, 'unit', u.code, 'source_unit', su.code))::jsonb AS row,
  x.ingested_at, x.normalized_at, x.superseded_at, x.deleted_at,
  p.code AS provider, x.connection_id, cn.mode AS connection_mode,
  nv.name AS normalizer_name, nv.version AS normalizer_version, nv.git_sha AS normalizer_git_sha,
  r.id AS raw_id, r.stream AS raw_stream, r.external_key AS raw_external_key, r.version AS raw_version,
  r.content_sha256 AS raw_content_sha256, r.content_type AS raw_content_type, bl.size_bytes AS raw_size_bytes,
  r.fetched_at AS raw_fetched_at, r.stored_at AS raw_stored_at, r.request_meta AS raw_request_meta,
  r.shape_fingerprint AS raw_shape_fingerprint, r.status AS raw_status,
  ib.id AS batch_id, ib.source_kind AS batch_source_kind, ib.migration_source AS batch_migration_source,
  ib.idempotency_key AS batch_idempotency_key, ib.received_at AS batch_received_at,
  cl.id AS client_id, cl.kind AS client_kind, cl.name AS client_name,
  x.deleted_by_raw_id, dr.fetched_at AS deleted_by_fetched_at
FROM chain c
JOIN measurements x ON x.id = c.id
JOIN providers p ON p.id = x.provider_id
JOIN connections cn ON cn.id = x.connection_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN raw_payloads r ON r.id = x.raw_payload_id
LEFT JOIN blobs bl ON bl.sha256 = r.content_sha256
LEFT JOIN ingest_batches ib ON ib.id = r.batch_id
LEFT JOIN clients cl ON cl.id = ib.client_id
LEFT JOIN raw_payloads dr ON dr.id = x.deleted_by_raw_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
JOIN metric_catalog mc ON mc.id = x.metric_id
JOIN units u ON u.id = mc.unit_id
LEFT JOIN units su ON su.id = x.source_unit_id
ORDER BY c.depth;

-- name: TraceGroupChain :many
-- Every version of one measurement group (the given row, its predecessors and its successors along
-- superseded_by), oldest first, each with its provenance. depth is 0 for the given row.
WITH RECURSIVE
back(id, depth) AS (
  SELECT t.id, 0 FROM measurement_groups t WHERE t.id = @id
  UNION ALL
  SELECT t.id, b.depth - 1 FROM measurement_groups t JOIN back b ON t.superseded_by = b.id
),
fwd(id, depth) AS (
  SELECT t.id, 0 FROM measurement_groups t WHERE t.id = @id
  UNION ALL
  SELECT t.superseded_by, f.depth + 1 FROM measurement_groups t JOIN fwd f ON t.id = f.id WHERE t.superseded_by IS NOT NULL
),
chain AS (SELECT id, depth FROM back UNION SELECT id, depth FROM fwd)
SELECT
  x.id::text AS id,
  COALESCE(x.superseded_by::text, '')::text AS superseded_by,
  c.depth::integer AS depth,
  (to_jsonb(x) - 'dedupe_key' || jsonb_build_object('dedupe_key', encode(x.dedupe_key, 'hex'), 'device', d.fingerprint, 'origin', o.origin_key, 'components', (SELECT coalesce(jsonb_agg(m.id ORDER BY m.id), '[]') FROM measurements m WHERE m.group_id = x.id)))::jsonb AS row,
  x.ingested_at, x.normalized_at, x.superseded_at, x.deleted_at,
  p.code AS provider, x.connection_id, cn.mode AS connection_mode,
  nv.name AS normalizer_name, nv.version AS normalizer_version, nv.git_sha AS normalizer_git_sha,
  r.id AS raw_id, r.stream AS raw_stream, r.external_key AS raw_external_key, r.version AS raw_version,
  r.content_sha256 AS raw_content_sha256, r.content_type AS raw_content_type, bl.size_bytes AS raw_size_bytes,
  r.fetched_at AS raw_fetched_at, r.stored_at AS raw_stored_at, r.request_meta AS raw_request_meta,
  r.shape_fingerprint AS raw_shape_fingerprint, r.status AS raw_status,
  ib.id AS batch_id, ib.source_kind AS batch_source_kind, ib.migration_source AS batch_migration_source,
  ib.idempotency_key AS batch_idempotency_key, ib.received_at AS batch_received_at,
  cl.id AS client_id, cl.kind AS client_kind, cl.name AS client_name,
  x.deleted_by_raw_id, dr.fetched_at AS deleted_by_fetched_at
FROM chain c
JOIN measurement_groups x ON x.id = c.id
JOIN providers p ON p.id = x.provider_id
JOIN connections cn ON cn.id = x.connection_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN raw_payloads r ON r.id = x.raw_payload_id
LEFT JOIN blobs bl ON bl.sha256 = r.content_sha256
LEFT JOIN ingest_batches ib ON ib.id = r.batch_id
LEFT JOIN clients cl ON cl.id = ib.client_id
LEFT JOIN raw_payloads dr ON dr.id = x.deleted_by_raw_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
ORDER BY c.depth;

-- name: TraceSleepChain :many
-- Every version of one sleep session (the given row, its predecessors and its successors along
-- superseded_by), oldest first, each with its provenance. depth is 0 for the given row.
WITH RECURSIVE
back(id, depth) AS (
  SELECT t.id, 0 FROM sleep_sessions t WHERE t.id = @id
  UNION ALL
  SELECT t.id, b.depth - 1 FROM sleep_sessions t JOIN back b ON t.superseded_by = b.id
),
fwd(id, depth) AS (
  SELECT t.id, 0 FROM sleep_sessions t WHERE t.id = @id
  UNION ALL
  SELECT t.superseded_by, f.depth + 1 FROM sleep_sessions t JOIN fwd f ON t.id = f.id WHERE t.superseded_by IS NOT NULL
),
chain AS (SELECT id, depth FROM back UNION SELECT id, depth FROM fwd)
SELECT
  x.id::text AS id,
  COALESCE(x.superseded_by::text, '')::text AS superseded_by,
  c.depth::integer AS depth,
  (to_jsonb(x) - 'dedupe_key' || jsonb_build_object('dedupe_key', encode(x.dedupe_key, 'hex'), 'device', d.fingerprint, 'origin', o.origin_key, 'stages', (SELECT coalesce(jsonb_agg(jsonb_build_object('stage', st.stage, 'start_at', st.start_at, 'end_at', st.end_at) ORDER BY st.start_at, st.id), '[]') FROM sleep_stages st WHERE st.session_id = x.id)))::jsonb AS row,
  x.ingested_at, x.normalized_at, x.superseded_at, x.deleted_at,
  p.code AS provider, x.connection_id, cn.mode AS connection_mode,
  nv.name AS normalizer_name, nv.version AS normalizer_version, nv.git_sha AS normalizer_git_sha,
  r.id AS raw_id, r.stream AS raw_stream, r.external_key AS raw_external_key, r.version AS raw_version,
  r.content_sha256 AS raw_content_sha256, r.content_type AS raw_content_type, bl.size_bytes AS raw_size_bytes,
  r.fetched_at AS raw_fetched_at, r.stored_at AS raw_stored_at, r.request_meta AS raw_request_meta,
  r.shape_fingerprint AS raw_shape_fingerprint, r.status AS raw_status,
  ib.id AS batch_id, ib.source_kind AS batch_source_kind, ib.migration_source AS batch_migration_source,
  ib.idempotency_key AS batch_idempotency_key, ib.received_at AS batch_received_at,
  cl.id AS client_id, cl.kind AS client_kind, cl.name AS client_name,
  x.deleted_by_raw_id, dr.fetched_at AS deleted_by_fetched_at
FROM chain c
JOIN sleep_sessions x ON x.id = c.id
JOIN providers p ON p.id = x.provider_id
JOIN connections cn ON cn.id = x.connection_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN raw_payloads r ON r.id = x.raw_payload_id
LEFT JOIN blobs bl ON bl.sha256 = r.content_sha256
LEFT JOIN ingest_batches ib ON ib.id = r.batch_id
LEFT JOIN clients cl ON cl.id = ib.client_id
LEFT JOIN raw_payloads dr ON dr.id = x.deleted_by_raw_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
ORDER BY c.depth;

-- name: TraceWorkoutChain :many
-- Every version of one workout (the given row, its predecessors and its successors along
-- superseded_by), oldest first, each with its provenance. depth is 0 for the given row.
WITH RECURSIVE
back(id, depth) AS (
  SELECT t.id, 0 FROM workouts t WHERE t.id = @id
  UNION ALL
  SELECT t.id, b.depth - 1 FROM workouts t JOIN back b ON t.superseded_by = b.id
),
fwd(id, depth) AS (
  SELECT t.id, 0 FROM workouts t WHERE t.id = @id
  UNION ALL
  SELECT t.superseded_by, f.depth + 1 FROM workouts t JOIN fwd f ON t.id = f.id WHERE t.superseded_by IS NOT NULL
),
chain AS (SELECT id, depth FROM back UNION SELECT id, depth FROM fwd)
SELECT
  x.id::text AS id,
  COALESCE(x.superseded_by::text, '')::text AS superseded_by,
  c.depth::integer AS depth,
  (to_jsonb(x) - 'dedupe_key' || jsonb_build_object('dedupe_key', encode(x.dedupe_key, 'hex'), 'device', d.fingerprint, 'origin', o.origin_key, 'segments', (SELECT coalesce(jsonb_agg(jsonb_build_object('seq', sg.seq, 'kind', sg.kind, 'start_at', sg.start_at, 'end_at', sg.end_at, 'data', sg.data) ORDER BY sg.seq), '[]') FROM workout_segments sg WHERE sg.workout_id = x.id)))::jsonb AS row,
  x.ingested_at, x.normalized_at, x.superseded_at, x.deleted_at,
  p.code AS provider, x.connection_id, cn.mode AS connection_mode,
  nv.name AS normalizer_name, nv.version AS normalizer_version, nv.git_sha AS normalizer_git_sha,
  r.id AS raw_id, r.stream AS raw_stream, r.external_key AS raw_external_key, r.version AS raw_version,
  r.content_sha256 AS raw_content_sha256, r.content_type AS raw_content_type, bl.size_bytes AS raw_size_bytes,
  r.fetched_at AS raw_fetched_at, r.stored_at AS raw_stored_at, r.request_meta AS raw_request_meta,
  r.shape_fingerprint AS raw_shape_fingerprint, r.status AS raw_status,
  ib.id AS batch_id, ib.source_kind AS batch_source_kind, ib.migration_source AS batch_migration_source,
  ib.idempotency_key AS batch_idempotency_key, ib.received_at AS batch_received_at,
  cl.id AS client_id, cl.kind AS client_kind, cl.name AS client_name,
  x.deleted_by_raw_id, dr.fetched_at AS deleted_by_fetched_at
FROM chain c
JOIN workouts x ON x.id = c.id
JOIN providers p ON p.id = x.provider_id
JOIN connections cn ON cn.id = x.connection_id
JOIN normalizer_versions nv ON nv.id = x.normalizer_version_id
LEFT JOIN raw_payloads r ON r.id = x.raw_payload_id
LEFT JOIN blobs bl ON bl.sha256 = r.content_sha256
LEFT JOIN ingest_batches ib ON ib.id = r.batch_id
LEFT JOIN clients cl ON cl.id = ib.client_id
LEFT JOIN raw_payloads dr ON dr.id = x.deleted_by_raw_id
LEFT JOIN devices d ON d.id = x.device_id
LEFT JOIN data_origins o ON o.id = x.origin_id
ORDER BY c.depth;
