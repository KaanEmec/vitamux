-- Source setup in the web panel (E20, ADR-0021): provider app credentials and panel sidecars.

-- name: GetProviderApp :one
SELECT client_id, ciphertext, updated_at FROM provider_app_credentials WHERE provider = @provider;

-- name: UpsertProviderApp :one
-- Returns whether a stored value was replaced.
INSERT INTO provider_app_credentials (provider, client_id, ciphertext, key_id, updated_by)
VALUES (@provider, @client_id, @ciphertext, @key_id, @updated_by)
ON CONFLICT (provider) DO UPDATE
SET client_id = excluded.client_id, ciphertext = excluded.ciphertext, key_id = excluded.key_id,
    updated_at = now(), updated_by = excluded.updated_by
RETURNING (xmax <> 0)::boolean AS replaced;

-- name: DeleteProviderApp :execrows
DELETE FROM provider_app_credentials WHERE provider = @provider;

-- name: CountAuthorizedConnections :one
-- Connections of a provider that hold tokens, which refresh with the provider's app credentials.
SELECT count(*)::int FROM connections c
JOIN providers p ON p.id = c.provider_id
JOIN credentials cr ON cr.connection_id = c.id
WHERE p.code = @provider;

-- name: ProviderConnectionCounts :many
-- The user's connections per provider that are not disconnected.
SELECT p.code AS provider, count(*)::int AS connections
FROM connections c JOIN providers p ON p.id = c.provider_id
WHERE c.user_id = @user_id AND c.status <> 'disabled'
GROUP BY p.code;

-- name: CountProviderConnections :one
-- Every connection of a provider, whatever its status (a removed sidecar strands them).
SELECT count(*)::int FROM connections c JOIN providers p ON p.id = c.provider_id WHERE p.code = @provider;

-- name: ListSidecars :many
SELECT name, url, ciphertext, created_at FROM sidecars ORDER BY name;

-- name: InsertSidecar :execrows
INSERT INTO sidecars (name, url, ciphertext, key_id, created_by)
VALUES (@name, @url, @ciphertext, @key_id, @created_by)
ON CONFLICT (name) DO NOTHING;

-- name: DeleteSidecar :execrows
DELETE FROM sidecars WHERE name = @name;

-- name: LockProviderAppsToRotate :many
SELECT provider, ciphertext FROM provider_app_credentials
WHERE key_id <> @key_id ORDER BY provider LIMIT @batch FOR UPDATE SKIP LOCKED;

-- name: ResealProviderApp :exec
UPDATE provider_app_credentials SET ciphertext = @ciphertext, key_id = @key_id WHERE provider = @provider;

-- name: LockSidecarsToRotate :many
SELECT name, ciphertext FROM sidecars
WHERE key_id <> @key_id ORDER BY name LIMIT @batch FOR UPDATE SKIP LOCKED;

-- name: ResealSidecar :exec
UPDATE sidecars SET ciphertext = @ciphertext, key_id = @key_id WHERE name = @name;
