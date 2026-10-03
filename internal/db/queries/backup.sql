-- Backup and restore (J13.5, docs/operations/backup.md).

-- name: CountSchemaTables :one
-- A restore target must have no tables yet (roles.sql creates the empty schema).
SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname = 'vitamux';

-- name: ListReferencedBlobHashes :many
-- Rows with refcount 0 are sweep candidates; their files may be gone in a consistent backup.
SELECT sha256 FROM blobs WHERE refcount > 0 ORDER BY sha256;

-- name: RevokeAppDefaultPrivileges :exec
-- Before pg_restore: objects it creates must start with the default ACL, because the dump
-- only lists differences from it (otherwise every narrowed table would regain full DML).
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  REVOKE ALL ON TABLES FROM vitamux_app;

-- name: RevokeAppDefaultSequencePrivileges :exec
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  REVOKE ALL ON SEQUENCES FROM vitamux_app;

-- name: RevokeAppDefaultFunctionPrivileges :exec
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  REVOKE ALL ON FUNCTIONS FROM vitamux_app;

-- name: GrantAppDefaultPrivileges :exec
-- After pg_restore: the defaults of deploy/sql/roles.sql again.
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO vitamux_app;

-- name: GrantAppDefaultSequencePrivileges :exec
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  GRANT USAGE, SELECT ON SEQUENCES TO vitamux_app;

-- name: GrantAppDefaultFunctionPrivileges :exec
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  GRANT EXECUTE ON FUNCTIONS TO vitamux_app;
