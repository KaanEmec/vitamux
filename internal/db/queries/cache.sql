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

-- name: DeleteHourlyAggregates :exec
DELETE FROM source_hourly_aggregates
WHERE user_id = @user_id AND metric_id = @metric_id AND hour_start >= @from_at AND hour_start < @to_at;

-- name: DisableJIT :exec
-- For the rest of the transaction: compiling RebuildHourlyAggregates (whose estimates are large)
-- costs more than it saves.
SELECT set_config('jit', 'off', true);

-- name: RebuildHourlyAggregates :execrows
-- Aggregates the active non-daily rows of a metric that start from from_at into the owner's
-- local hours (hour_starts, hour_ends, local_dates: ascending and contiguous, ending at to_at), in
-- the database so a year of dense rows never leaves it. A sample counts in the hour it starts, an
-- interval in every hour it overlaps, pro-rated linearly by its nanoseconds in the hour. buckets
-- counts the UTC-aligned 5-minute buckets holding a sample or part of an interval and
-- bucket_mean_sum adds each bucket's sample mean in bucket order; sums run in (start_at, id)
-- order, so the floats are reproducible. The caller deletes the hours first.
WITH h AS (
  SELECT @hour_starts::timestamptz[] AS hs, @hour_ends::timestamptz[] AS he, @local_dates::date[] AS ld
), sample_bucket AS ( -- samples per source, hour i and 5-minute bucket b
  SELECT s.connection_id, s.device_id, s.origin_id, s.i, s.b, count(*) AS n, sum(s.value ORDER BY s.start_at, s.id) AS total,
    min(s.value) AS min_value, max(s.value) AS max_value, min(s.start_at) AS first_at, max(s.start_at) AS last_at
  FROM (
    SELECT x.id, x.start_at, x.value, x.connection_id, x.device_id, x.origin_id, width_bucket(x.start_at, h.hs) AS i,
      date_bin('5 minutes', x.start_at, TIMESTAMPTZ '2000-01-01 00:00:00+00') AS b
    FROM h, measurements x
    WHERE x.user_id = @user_id AND x.metric_id = @metric_id
      AND x.start_at >= @from_at AND x.start_at < @to_at
      AND x.superseded_at IS NULL AND x.deleted_at IS NULL AND x.kind <> 'daily_value'
      AND (x.end_at IS NULL OR x.end_at <= x.start_at)
  ) s, h
  WHERE s.i > 0 AND s.start_at < h.he[s.i]
  GROUP BY s.connection_id, s.device_id, s.origin_id, s.i, s.b
), interval_part AS ( -- an interval's part [lo, hi) in each hour i it overlaps
  SELECT p.* FROM (
    SELECT x.id, x.start_at, x.end_at, x.value, x.connection_id, x.device_id, x.origin_id, i,
      greatest(x.start_at, h.hs[i]) AS lo, least(x.end_at, h.he[i]) AS hi
    FROM h, measurements x,
      generate_series(width_bucket(greatest(x.start_at, h.hs[1]), h.hs), width_bucket(x.end_at - interval '1 microsecond', h.hs)) AS i
    WHERE x.user_id = @user_id AND x.metric_id = @metric_id
      AND x.start_at >= @from_at AND x.start_at < @to_at
      AND x.superseded_at IS NULL AND x.deleted_at IS NULL AND x.kind <> 'daily_value'
      AND x.end_at > x.start_at
  ) p
  WHERE p.lo < p.hi
), bucket AS ( -- every bucket holding a sample or part of an interval
  SELECT connection_id, device_id, origin_id, i, b FROM sample_bucket
  UNION
  SELECT p.connection_id, p.device_id, p.origin_id, p.i, g
  FROM interval_part p,
    generate_series(date_bin('5 minutes', p.lo, TIMESTAMPTZ '2000-01-01 00:00:00+00'), p.hi - interval '1 microsecond', interval '5 minutes') AS g
), hour AS ( -- per source and hour: the samples' part, the intervals' part, the bucket count
  SELECT connection_id, device_id, origin_id, i, sum(n)::integer AS samples, NULL::integer AS buckets,
    sum(total / n ORDER BY b) AS bucket_mean_sum, min(min_value) AS min_value, max(max_value) AS max_value,
    NULL::double precision AS interval_sum, min(first_at) AS first_at, max(last_at) AS last_at
  FROM sample_bucket GROUP BY connection_id, device_id, origin_id, i
  UNION ALL
  SELECT connection_id, device_id, origin_id, i, count(*)::integer, NULL, NULL, min(value), max(value),
    sum(value * (extract(epoch FROM hi - lo) * 1000000000)::float8 / (extract(epoch FROM end_at - start_at) * 1000000000)::float8
        ORDER BY start_at, id),
    min(lo), max(hi)
  FROM interval_part GROUP BY connection_id, device_id, origin_id, i
  UNION ALL
  SELECT connection_id, device_id, origin_id, i, 0, count(*)::integer, NULL, NULL, NULL, NULL, NULL, NULL
  FROM bucket GROUP BY connection_id, device_id, origin_id, i
)
INSERT INTO source_hourly_aggregates (user_id, metric_id, source_key, hour_start, local_date, connection_id, device_id,
                                      origin_id, samples, buckets, bucket_mean_sum, min_value, max_value, interval_sum,
                                      first_at, last_at)
SELECT @user_id, @metric_id,
  a.connection_id::text || '/' || COALESCE(a.device_id::text, '-') || '/' || COALESCE(a.origin_id::text, '-'),
  h.hs[a.i], h.ld[a.i], a.connection_id, a.device_id, a.origin_id, a.samples, a.buckets, COALESCE(a.bucket_mean_sum, 0),
  a.min_value, a.max_value, COALESCE(a.interval_sum, 0), a.first_at, a.last_at
FROM (
  SELECT connection_id, device_id, origin_id, i, sum(samples)::integer AS samples, sum(buckets)::smallint AS buckets,
    sum(bucket_mean_sum) AS bucket_mean_sum, min(min_value) AS min_value, max(max_value) AS max_value,
    sum(interval_sum) AS interval_sum, min(first_at) AS first_at, max(last_at) AS last_at
  FROM hour GROUP BY connection_id, device_id, origin_id, i
) a, h;

-- name: ListResolveUsers :many
SELECT id FROM users ORDER BY id;
