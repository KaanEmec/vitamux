-- Runs once, right after deploy/sql/roles.sql, on a new pgdata volume. Passwords come from the
-- Compose secrets (backtick reads strip the trailing newline); they never appear in argv or logs.
\set owner_pw `cat /run/secrets/pg_owner_password`
\set app_pw `cat /run/secrets/pg_app_password`
ALTER ROLE vitamux_owner LOGIN PASSWORD :'owner_pw';
ALTER ROLE vitamux_app LOGIN PASSWORD :'app_pw';
