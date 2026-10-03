-- Manual overrides (J09.7). Rows are never deleted; a revoke sets revoked_at.

-- name: InsertOverride :one
INSERT INTO manual_overrides (id, user_id, metric, window_kind, window_key, local_date, action,
                              input_id, source_group, value, unit, note, created_by)
VALUES (@id, @user_id, @metric, @window_kind, @window_key, @local_date, @action,
        @input_id, @source_group, @value, @unit, @note, @created_by)
RETURNING *;

-- name: GetOverride :one
SELECT * FROM manual_overrides WHERE id = @id AND user_id = @user_id;

-- name: RevokeOverride :one
UPDATE manual_overrides SET revoked_at = now(), revoked_by = @revoked_by
WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL
RETURNING *;

-- name: ListActiveOverrides :many
SELECT * FROM manual_overrides
WHERE user_id = @user_id AND metric = @metric AND revoked_at IS NULL
  AND local_date BETWEEN @from_date::date AND @to_date::date
ORDER BY created_at, id;

-- name: ListOverrideHistory :many
SELECT * FROM manual_overrides
WHERE user_id = @user_id AND metric = @metric
ORDER BY created_at DESC, id DESC
LIMIT @max_rows::integer;

-- name: MarkOverrideDirty :exec
INSERT INTO resolution_dirty (user_id, metric_id, local_date)
SELECT @user_id::uuid, id, @local_date::date FROM metric_catalog WHERE code = ANY(@codes::text[])
ON CONFLICT (user_id, metric_id, local_date) DO UPDATE SET marked_at = now();
