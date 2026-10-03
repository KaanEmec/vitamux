-- Resolution rule versions (J09.2). Rows of resolution_rules are never updated.

-- name: LatestRuleVersion :one
SELECT coalesce(max(version), 0)::integer AS version
FROM resolution_rules WHERE user_id = @user_id AND metric = @metric;

-- name: InsertRuleVersion :one
INSERT INTO resolution_rules (id, user_id, metric, version, spec, based_on, note, created_by)
VALUES (@id, @user_id, @metric, @version, @spec, @based_on, @note, @created_by)
RETURNING *;

-- name: GetRuleVersion :one
SELECT * FROM resolution_rules WHERE user_id = @user_id AND metric = @metric AND version = @version;

-- name: ListRuleVersions :many
SELECT sqlc.embed(r), (a.version IS NOT NULL)::boolean AS active
FROM resolution_rules r
LEFT JOIN active_rules a ON a.user_id = r.user_id AND a.metric = r.metric AND a.version = r.version
WHERE r.user_id = @user_id AND r.metric = @metric
ORDER BY r.version DESC;

-- name: ListActiveRules :many
SELECT r.* FROM active_rules a
JOIN resolution_rules r ON r.user_id = a.user_id AND r.metric = a.metric AND r.version = a.version
WHERE a.user_id = @user_id
ORDER BY r.metric;

-- name: SetActiveRule :exec
INSERT INTO active_rules (user_id, metric, version, activated_by)
VALUES (@user_id, @metric, @version, @activated_by)
ON CONFLICT (user_id, metric) DO UPDATE
SET version = EXCLUDED.version, activated_by = EXCLUDED.activated_by, activated_at = now();
