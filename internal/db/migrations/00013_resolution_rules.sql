-- Resolution rule versions (J09.2; docs/architecture/resolution.md#selectors-and-validation).
-- Built-in defaults live in code (internal/resolve/builtin.go) and apply while a metric has no
-- active_rules row; the first edit copies the built-in into version 1.

-- +goose Up
CREATE TABLE resolution_rules (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  metric     text NOT NULL CHECK (metric ~ '^[a-z][a-z0-9_]*$'),
  version    integer NOT NULL CHECK (version >= 1),
  spec       jsonb NOT NULL,
  based_on   text CHECK (based_on ~ '^builtin:[a-z][a-z0-9_]*:[1-9][0-9]*$'),
  note       text,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, metric, version)
);
COMMENT ON TABLE resolution_rules IS 'Immutable rule versions; an edit inserts version + 1. Insert-only for the app role.';
COMMENT ON COLUMN resolution_rules.metric IS 'Catalogue code or rule family (sleep, blood_pressure), so no foreign key to metric_catalog.';
COMMENT ON COLUMN resolution_rules.spec IS 'Rule JSON, schemas/resolution-rule.v1.json; validated by internal/resolve before insert.';
COMMENT ON COLUMN resolution_rules.based_on IS 'Built-in reference this version copied (builtin:<metric>:<n>); set on the copy only.';
COMMENT ON COLUMN resolution_rules.created_by IS 'Audit actor: owner, api_key:<id> or system.';
REVOKE UPDATE, DELETE ON resolution_rules FROM vitamux_app;

CREATE TABLE active_rules (
  user_id      uuid NOT NULL,
  metric       text NOT NULL,
  version      integer NOT NULL,
  activated_by text NOT NULL,
  activated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, metric),
  FOREIGN KEY (user_id, metric, version) REFERENCES resolution_rules (user_id, metric, version) ON DELETE CASCADE
);
COMMENT ON TABLE active_rules IS 'The rule version in effect per metric. No row: the built-in default applies, if there is one.';

-- +goose Down
DROP TABLE active_rules, resolution_rules;
