-- Sync state and job queue (J02.3); see docs/architecture/reliability.md#job-queue.

-- +goose Up
CREATE TABLE schedules (
  id            uuid PRIMARY KEY,
  connection_id uuid NOT NULL REFERENCES connections ON DELETE CASCADE,
  stream        text NOT NULL,
  run_interval  interval NOT NULL CHECK (run_interval > interval '0'),
  lookback      interval NOT NULL DEFAULT interval '0' CHECK (lookback >= interval '0'),
  next_run_at   timestamptz NOT NULL,
  enabled       boolean NOT NULL DEFAULT true,
  UNIQUE (connection_id, stream)
);
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE enabled;

CREATE TABLE jobs (
  id               uuid PRIMARY KEY,
  kind             text NOT NULL CHECK (kind IN ('sync', 'backfill_unit', 'normalize_batch', 'reprocess',
                     'rebuild_aggregates', 'extract_document', 'export', 'backup', 'import_unit',
                     'prune_retention', 'sweep_blobs', 'recompute_local_dates')),
  status           text NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'dead', 'cancelled')),
  connection_id    uuid REFERENCES connections ON DELETE CASCADE,
  exclusive        boolean NOT NULL DEFAULT false,
  priority         smallint NOT NULL DEFAULT 100,
  run_at           timestamptz NOT NULL DEFAULT now(),
  dedupe_key       text,
  payload          jsonb NOT NULL DEFAULT '{}',
  attempts         integer NOT NULL DEFAULT 0,
  max_attempts     integer NOT NULL DEFAULT 5,
  lease_owner      text,
  lease_expires_at timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  started_at       timestamptz,
  finished_at      timestamptz,
  CONSTRAINT jobs_exclusive_check CHECK (NOT exclusive OR connection_id IS NOT NULL),
  CONSTRAINT jobs_lease_check CHECK (status <> 'running' OR (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL))
);
COMMENT ON COLUMN jobs.priority IS 'Lower runs first.';
COMMENT ON COLUMN jobs.payload IS 'Job parameters. Never secrets, passwords or MFA codes.';
CREATE UNIQUE INDEX jobs_one_exclusive_running_idx ON jobs (connection_id) WHERE status = 'running' AND exclusive;
CREATE UNIQUE INDEX jobs_active_dedupe_idx ON jobs (dedupe_key) WHERE status IN ('queued', 'running');
CREATE INDEX jobs_claim_idx ON jobs (priority, run_at) WHERE status = 'queued';
CREATE INDEX jobs_connection_idx ON jobs (connection_id, created_at);

CREATE TABLE job_runs (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  job_id        uuid NOT NULL REFERENCES jobs ON DELETE CASCADE,
  attempt       integer NOT NULL,
  started_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz,
  outcome       text CHECK (outcome IN ('succeeded', 'failed', 'rescheduled', 'lease_expired', 'cancelled')),
  error_class   text,
  error_message text,
  stats         jsonb NOT NULL DEFAULT '{}'
);
COMMENT ON TABLE job_runs IS 'One row per execution; outcome is null while running. Kept 90 days.';
COMMENT ON COLUMN job_runs.error_message IS 'Sanitized: no secrets, bodies or health values.';
CREATE INDEX job_runs_job_idx ON job_runs (job_id);

CREATE TABLE sync_cursors (
  connection_id  uuid NOT NULL REFERENCES connections ON DELETE CASCADE,
  stream         text NOT NULL,
  cursor         jsonb,
  high_watermark timestamptz,
  status         text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'degraded')),
  status_reason  text,
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (connection_id, stream)
);
COMMENT ON COLUMN sync_cursors.status_reason IS 'Why the stream is degraded, e.g. schema_drift.';

CREATE TABLE backfills (
  id            uuid PRIMARY KEY,
  connection_id uuid NOT NULL REFERENCES connections ON DELETE CASCADE,
  stream        text NOT NULL,
  range_start   timestamptz NOT NULL,
  range_end     timestamptz NOT NULL,
  status        text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done', 'failed', 'cancelled')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  finished_at   timestamptz,
  CHECK (range_end > range_start)
);

CREATE TABLE backfill_units (
  backfill_id      uuid NOT NULL REFERENCES backfills ON DELETE CASCADE,
  range_start      timestamptz NOT NULL,
  range_end        timestamptz NOT NULL,
  status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done', 'failed')),
  attempts         integer NOT NULL DEFAULT 0,
  last_error_class text,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (backfill_id, range_start),
  CHECK (range_end > range_start)
);

CREATE TABLE provider_rate_state (
  provider_id   smallint PRIMARY KEY REFERENCES providers,
  blocked_until timestamptz NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE provider_rate_state IS 'Shared Retry-After state, so a restart keeps honouring provider rate limits.';

-- +goose Down
DROP TABLE provider_rate_state, backfill_units, backfills, sync_cursors, job_runs, jobs, schedules;
