-- The first edit of a metric without a built-in copies its default rule (J24.1;
-- internal/resolve/default.go), so based_on also takes default:<metric>:<hash>.

-- +goose Up
ALTER TABLE resolution_rules DROP CONSTRAINT resolution_rules_based_on_check,
  ADD CONSTRAINT resolution_rules_based_on_check
    CHECK (based_on ~ '^(builtin:[a-z][a-z0-9_]*:[1-9][0-9]*|default:[a-z][a-z0-9_]*:[0-9a-f]{8})$');
COMMENT ON COLUMN resolution_rules.based_on IS 'Built-in (builtin:<metric>:<n>) or default rule (default:<metric>:<hash>) this version copied; set on the copy only.';

-- +goose Down
ALTER TABLE resolution_rules DROP CONSTRAINT resolution_rules_based_on_check,
  ADD CONSTRAINT resolution_rules_based_on_check CHECK (based_on ~ '^builtin:[a-z][a-z0-9_]*:[1-9][0-9]*$') NOT VALID;
COMMENT ON COLUMN resolution_rules.based_on IS 'Built-in reference this version copied (builtin:<metric>:<n>); set on the copy only.';
