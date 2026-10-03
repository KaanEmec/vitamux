-- Migration conventions (expand/contract): docs/development.md#migrations.

-- +goose Up
-- Only `vitamux migrate` may record schema versions.
REVOKE INSERT, UPDATE, DELETE ON goose_db_version FROM vitamux_app;

-- +goose Down
GRANT INSERT, UPDATE, DELETE ON goose_db_version TO vitamux_app;
