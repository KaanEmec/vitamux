-- Provider notification hooks (J08.4); see docs/providers/withings.md#notifications.

-- name: GetHookConnection :one
-- The connection a notification callback token belongs to; disconnected connections have none.
SELECT c.id, c.user_id, c.status, c.account_key FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.hook_token_hash = @hook_token_hash AND p.code = @provider AND c.status <> 'disabled';

-- name: ListHookConnections :many
SELECT c.id, c.status, c.hook_token_hash FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.user_id = @user_id AND p.code = @provider
ORDER BY c.id;

-- name: SetHookTokenHash :exec
UPDATE connections SET hook_token_hash = sqlc.narg(hook_token_hash), updated_at = now() WHERE id = @id;

-- name: GetUserSetting :one
SELECT value FROM settings WHERE user_id = @user_id AND key = @key;

-- name: PutUserSetting :exec
INSERT INTO settings (user_id, key, value) VALUES (@user_id, @key, @value)
ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = now();
