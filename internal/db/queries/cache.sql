-- Resolution cache and hourly aggregates (J09.9, internal/resolve/cache.go and aggregates.go).
-- Invalidation is done by the triggers of the resolution_cache migration.

-- name: GetResolvedCache :many
-- The cached dates of a request: each date with its overrides fingerprint, closed by now.
SELECT c.local_date, c.results FROM resolved_cache c
JOIN (SELECT unnest(@dates::date[]) AS d, unnest(@fps::text[]) AS fp) AS k ON c.local_date = k.d AND c.overrides_fp = k.fp
WHERE c.user_id = @user_id AND c.metric = @metric AND c.window_kind = @window_kind AND c.rule_ref = @rule_ref
  AND c.complete_at <= @now;

-- name: PutResolvedCache :many
-- Stores computed dates unless a dirty mark of a dependency is still pending in their range: a
-- request that read rows before a writer committed must not cache them. Returns the stored dates.
INSERT INTO resolved_cache (user_id, metric, window_kind, local_date, rule_ref, overrides_fp, results, deps,
                            dep_from, dep_to, complete_at)
SELECT @user_id, @metric, @window_kind, k.d, @rule_ref, k.fp, k.res, @deps::text[],
       k.d - @back_days::integer, k.d + @ahead_days::integer, k.complete
FROM (SELECT unnest(@dates::date[]) AS d, unnest(@fps::text[]) AS fp, unnest(@results::jsonb[]) AS res,
             unnest(@complete_at::timestamptz[]) AS complete) AS k
WHERE NOT EXISTS (
  SELECT 1 FROM resolution_dirty r JOIN metric_catalog mc ON mc.id = r.metric_id
  WHERE r.user_id = @user_id AND mc.code = ANY(@deps::text[])
    AND r.local_date BETWEEN k.d - @back_days::integer AND k.d + @ahead_days::integer)
ON CONFLICT DO NOTHING
RETURNING local_date;

-- name: ClaimDirtyMarks :many
-- Dirty marks old enough to consume, locked for the rebuild transaction.
SELECT r.user_id, r.metric_id, mc.code AS metric, r.local_date, r.marked_at
FROM resolution_dirty r JOIN metric_catalog mc ON mc.id = r.metric_id
WHERE r.marked_at < @before
ORDER BY r.user_id, r.metric_id, r.local_date
LIMIT @max_rows::integer
FOR UPDATE OF r SKIP LOCKED;

-- name: DeleteDirtyMarks :execrows
-- Deletes consumed marks; a mark renewed meanwhile (newer marked_at) stays.
DELETE FROM resolution_dirty r
USING (SELECT unnest(@user_ids::uuid[]) AS u, unnest(@metric_ids::smallint[]) AS m, unnest(@dates::date[]) AS d,
             unnest(@marked_at::timestamptz[]) AS at) AS k
WHERE r.user_id = k.u AND r.metric_id = k.m AND r.local_date = k.d AND r.marked_at = k.at;

-- name: AggregateRows :many
-- Active non-daily rows of a metric starting from from_at and before to_at, with their source ids.
SELECT x.kind, x.start_at, x.end_at, x.value, x.connection_id, x.device_id, x.origin_id
FROM measurements x
WHERE x.user_id = @user_id AND x.metric_id = @metric_id
  AND x.start_at >= @from_at AND x.start_at < @to_at
  AND x.superseded_at IS NULL AND x.deleted_at IS NULL AND x.kind <> 'daily_value'
ORDER BY x.start_at, x.id;

-- name: DeleteHourlyAggregates :exec
DELETE FROM source_hourly_aggregates
WHERE user_id = @user_id AND metric_id = @metric_id AND hour_start >= @from_at AND hour_start < @to_at;

-- name: InsertHourlyAggregates :execrows
-- Rows arrive as a JSON array of source_hourly_aggregates records.
INSERT INTO source_hourly_aggregates
SELECT (p).* FROM jsonb_populate_recordset(NULL::source_hourly_aggregates, @batch::jsonb) p;

-- name: ListHourlyAggregates :many
SELECT * FROM source_hourly_aggregates
WHERE user_id = @user_id AND metric_id = (SELECT id FROM metric_catalog WHERE code = @metric::text)
  AND hour_start >= @from_at AND hour_start < @to_at
ORDER BY hour_start, source_key;

-- name: ListResolveUsers :many
SELECT id FROM users ORDER BY id;
