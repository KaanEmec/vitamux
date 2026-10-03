-- name: InsertAPIKey :one
INSERT INTO api_keys (id, user_id, name, secret_hash, scopes, expires_at)
VALUES (@id, @user_id, @name, @secret_hash, @scopes, @expires_at)
RETURNING *;

-- name: GetAPIKey :one
SELECT * FROM api_keys WHERE id = @id;

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE user_id = @user_id ORDER BY created_at DESC, id;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = @now::timestamptz WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = @now::timestamptz
WHERE id = @id AND (last_used_at IS NULL OR last_used_at < @before::timestamptz);

-- name: InsertClient :exec
INSERT INTO clients (id, user_id, connection_id, kind, name, token_hash)
VALUES (@id, @user_id, @connection_id, @kind, @name, @token_hash);

-- name: GetClient :one
SELECT * FROM clients WHERE id = @id;

-- name: TouchClient :exec
UPDATE clients SET last_seen_at = @now::timestamptz
WHERE id = @id AND (last_seen_at IS NULL OR last_seen_at < @before::timestamptz);
