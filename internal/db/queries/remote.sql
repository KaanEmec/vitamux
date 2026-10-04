-- Remote sidecar connectors (J17.2); see docs/architecture/connectors.md#remote-sidecar-mode.

-- name: RegisterProvider :exec
-- A sidecar's provider code; a known code keeps its name and id.
SELECT register_provider(@code::text, @name::text);

-- name: UpdateConnectionUpstream :many
-- Records a sidecar's upstream package on its connections; returns the ones that changed, with
-- the previous value, for the audit trail.
WITH old AS (
  SELECT c.id, c.upstream FROM connections c JOIN providers p ON p.id = c.provider_id
  WHERE p.code = @provider AND c.mode = 'remote' AND c.upstream IS DISTINCT FROM sqlc.narg(upstream)::jsonb
  FOR UPDATE OF c
)
UPDATE connections c SET upstream = sqlc.narg(upstream)::jsonb, updated_at = now()
FROM old WHERE c.id = old.id
RETURNING c.id, c.user_id, old.upstream AS previous;
