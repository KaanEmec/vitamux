-- Manual overrides (J09.7; docs/architecture/resolution.md#manual-overrides). Owner corrections
-- in the selection layer: they never touch source rows, and a revoke keeps the row as history.

-- +goose Up
CREATE TABLE manual_overrides (
  id           uuid PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  metric       text NOT NULL CHECK (metric ~ '^[a-z][a-z0-9_]*$'),
  window_kind  text NOT NULL CHECK (window_kind IN ('bucket', 'hour', 'local_day', 'local_night', 'sleep_episode', 'latest', 'reading')),
  window_key   text NOT NULL CHECK (window_key <> ''),
  local_date   date NOT NULL,
  action       text NOT NULL CHECK (action IN ('exclude_input', 'force_source', 'set_value')),
  input_id     bigint,
  source_group text,
  value        double precision CHECK (value NOT IN ('NaN', 'Infinity', '-Infinity')),
  unit         text,
  note         text,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  revoked_at   timestamptz,
  revoked_by   text,
  CHECK (CASE action
    WHEN 'exclude_input' THEN input_id IS NOT NULL AND source_group IS NULL AND value IS NULL AND unit IS NULL
    WHEN 'force_source'  THEN input_id IS NULL AND source_group IS NOT NULL AND value IS NULL AND unit IS NULL
    ELSE input_id IS NULL AND source_group IS NULL AND value IS NOT NULL AND unit IS NOT NULL AND note IS NOT NULL
  END),
  CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);
COMMENT ON TABLE manual_overrides IS 'Owner corrections scoped to (metric, window kind, window key). Never delete: a revoke sets revoked_at and the row stays as history.';
COMMENT ON COLUMN manual_overrides.metric IS 'Rule metric: catalogue code or rule family (sleep, blood_pressure), so no foreign key to metric_catalog.';
COMMENT ON COLUMN manual_overrides.window_key IS 'resolve.Window.Key: date for local_day and local_night, UTC start for bucket, hour and sleep_episode, as_of for latest, g:<id> or m:<id> for reading.';
COMMENT ON COLUMN manual_overrides.local_date IS 'Local date the window belongs to; the resolution_dirty mark written with the override.';
COMMENT ON COLUMN manual_overrides.input_id IS 'exclude_input: measurements.id. No foreign key; a correction replaces the row, and the override is then reported as ignored.';
COMMENT ON COLUMN manual_overrides.source_group IS 'force_source: the rule group id to use for the window.';
COMMENT ON COLUMN manual_overrides.value IS 'set_value: the window value in the metric''s canonical unit (unit).';
COMMENT ON COLUMN manual_overrides.note IS 'set_value: why; required.';
COMMENT ON COLUMN manual_overrides.created_by IS 'Audit actor: owner, api_key:<id> or system.';
CREATE UNIQUE INDEX manual_overrides_window_active_idx ON manual_overrides (user_id, metric, window_kind, window_key, action)
  WHERE revoked_at IS NULL AND action <> 'exclude_input';
CREATE UNIQUE INDEX manual_overrides_exclude_active_idx ON manual_overrides (user_id, metric, window_kind, window_key, input_id)
  WHERE revoked_at IS NULL AND action = 'exclude_input';
CREATE INDEX manual_overrides_date_idx ON manual_overrides (user_id, metric, local_date) WHERE revoked_at IS NULL;
REVOKE UPDATE, DELETE ON manual_overrides FROM vitamux_app;
GRANT UPDATE (revoked_at, revoked_by) ON manual_overrides TO vitamux_app;

-- +goose Down
DROP TABLE manual_overrides;
