-- OAuth connection flow (J08.2); see docs/architecture/connectors.md#oauth-connection-flow.

-- name: InsertOAuthState :exec
INSERT INTO oauth_states (id, user_id, session_id, provider_id, connection_id, expires_at, session, return_to, ticket_hash, start_url, binding)
SELECT @id, @user_id, @session_id, p.id, sqlc.narg(connection_id), @expires_at, sqlc.narg(session), @return_to,
  sqlc.narg(ticket_hash), sqlc.narg(start_url), sqlc.narg(binding)
FROM providers p WHERE p.code = @provider;

-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at <= now();

-- name: ConsumeOAuthState :one
-- Single use: the row is gone after the first callback or continue, whatever happens next. A
-- state of an ended session or another provider is refused.
DELETE FROM oauth_states o
USING providers p, sessions s
WHERE o.id = @id AND p.id = o.provider_id AND p.code = @provider AND o.expires_at > now()
  AND s.id = o.session_id AND s.expires_at > now()
RETURNING o.user_id, o.session_id, o.connection_id, o.session, o.return_to;

-- name: UseOAuthTicket :one
-- Single use: the start route clears the ticket; the state row stays for the callback. A
-- ticket issued before @issued_after, of another provider or of an ended session is refused.
UPDATE oauth_states o SET ticket_hash = NULL
FROM providers p, sessions s
WHERE o.ticket_hash = @ticket_hash AND p.id = o.provider_id AND p.code = @provider
  AND o.created_at > @issued_after AND o.expires_at > now()
  AND s.id = o.session_id AND s.expires_at > now()
RETURNING o.id, o.start_url::text AS start_url, o.binding::bytea AS binding;

-- name: GetConnectionAccount :one
SELECT c.account_key FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.id = @id AND c.user_id = @user_id AND p.code = @provider;

-- name: UpsertOAuthConnection :one
-- Reconnecting the same provider account reuses its connection (UNIQUE user, provider, account_key)
-- and makes it active; @status applies to a new connection only.
INSERT INTO connections (id, user_id, provider_id, account_key, mode, status, upstream)
SELECT @id, @user_id, p.id, @account_key, @mode, @status, sqlc.narg(upstream) FROM providers p WHERE p.code = @provider
ON CONFLICT (user_id, provider_id, account_key) DO UPDATE
SET status = 'active', last_error_class = NULL, consecutive_failures = 0,
    upstream = coalesce(EXCLUDED.upstream, connections.upstream), updated_at = now()
RETURNING id, (xmax = 0) AS created;

-- name: ReauthorizeConnection :exec
UPDATE connections
SET account_key = coalesce(account_key, @account_key), status = 'active', last_error_class = NULL,
    consecutive_failures = 0, upstream = coalesce(sqlc.narg(upstream), upstream), updated_at = now()
WHERE id = @id;

-- name: DisableConnection :execrows
UPDATE connections SET status = 'disabled', updated_at = now()
WHERE id = @id AND user_id = @user_id;

-- name: DeleteCredentials :exec
DELETE FROM credentials WHERE connection_id = @connection_id;
