-- OAuth connection flow (J08.2); see docs/architecture/connectors.md#oauth-connection-flow.

-- name: InsertOAuthState :exec
INSERT INTO oauth_states (id, user_id, session_id, provider_id, connection_id, expires_at)
SELECT @id, @user_id, @session_id, p.id, sqlc.narg(connection_id), @expires_at
FROM providers p WHERE p.code = @provider;

-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at <= now();

-- name: ConsumeOAuthState :one
-- Single use: the row is gone after the first callback, whatever happens next. A state of an
-- ended session or another provider is refused.
DELETE FROM oauth_states o
USING providers p, sessions s
WHERE o.id = @id AND p.id = o.provider_id AND p.code = @provider AND o.expires_at > now()
  AND s.id = o.session_id AND s.expires_at > now()
RETURNING o.user_id, o.connection_id;

-- name: GetConnectionAccount :one
SELECT c.account_key FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.id = @id AND c.user_id = @user_id AND p.code = @provider;

-- name: UpsertOAuthConnection :one
-- Reconnecting the same provider account reuses its connection (UNIQUE user, provider, account_key).
INSERT INTO connections (id, user_id, provider_id, account_key, mode, status)
SELECT @id, @user_id, p.id, @account_key, 'in_process', 'active' FROM providers p WHERE p.code = @provider
ON CONFLICT (user_id, provider_id, account_key) DO UPDATE
SET status = 'active', last_error_class = NULL, consecutive_failures = 0, updated_at = now()
RETURNING id, (xmax = 0) AS created;

-- name: ReauthorizeConnection :exec
UPDATE connections
SET account_key = coalesce(account_key, @account_key), status = 'active', last_error_class = NULL,
    consecutive_failures = 0, updated_at = now()
WHERE id = @id;

-- name: DisableConnection :execrows
UPDATE connections SET status = 'disabled', updated_at = now()
WHERE id = @id AND user_id = @user_id;

-- name: DeleteCredentials :exec
DELETE FROM credentials WHERE connection_id = @connection_id;
