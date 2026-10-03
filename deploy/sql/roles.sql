-- Vitamux database roles and schema. Run once per database as a superuser (or a role with
-- CREATEROLE that owns the database); it is idempotent:
--   psql "$ADMIN_URL" -v ON_ERROR_STOP=1 -f deploy/sql/roles.sql
-- Production: give each role a login and a password out of band, e.g.
--   ALTER ROLE vitamux_owner LOGIN PASSWORD '…';  ALTER ROLE vitamux_app LOGIN PASSWORD '…';
-- Development may log in as the superuser: vitamux switches to the right role per session.
-- See docs/architecture/security.md#database-roles.

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'vitamux_owner') THEN
    CREATE ROLE vitamux_owner NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'vitamux_app') THEN
    CREATE ROLE vitamux_app NOLOGIN;
  END IF;
END
$$;

CREATE SCHEMA IF NOT EXISTS vitamux AUTHORIZATION vitamux_owner;
REVOKE ALL ON SCHEMA vitamux FROM PUBLIC;
GRANT USAGE ON SCHEMA vitamux TO vitamux_app;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- The app role gets DML on everything the owner creates. Migrations narrow this per table
-- (e.g. audit_events is INSERT/SELECT only). No DDL, no TRUNCATE by default.
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO vitamux_app;
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  GRANT USAGE, SELECT ON SEQUENCES TO vitamux_app;
ALTER DEFAULT PRIVILEGES FOR ROLE vitamux_owner IN SCHEMA vitamux
  GRANT EXECUTE ON FUNCTIONS TO vitamux_app;
