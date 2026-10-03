-- Job checkpoints and correction schedules (J06.1, J06.3); see docs/architecture/reliability.md#scheduler.

-- +goose Up
ALTER TABLE jobs ADD COLUMN checkpoint jsonb;
COMMENT ON COLUMN jobs.checkpoint IS 'Progress saved by the handler; a re-claimed job resumes from it. Never secrets or health values.';

ALTER TABLE schedules
  ADD COLUMN mode text NOT NULL DEFAULT 'incremental' CHECK (mode IN ('incremental', 'correction')),
  DROP CONSTRAINT schedules_connection_id_stream_key,
  ADD CONSTRAINT schedules_connection_id_stream_mode_key UNIQUE (connection_id, stream, mode),
  ADD CONSTRAINT schedules_correction_lookback_check CHECK (mode <> 'correction' OR lookback > interval '0');
COMMENT ON COLUMN schedules.mode IS 'incremental follows the cursor; correction re-fetches the lookback window ending at each slot.';

-- +goose Down
DELETE FROM schedules WHERE mode = 'correction';
ALTER TABLE schedules
  DROP CONSTRAINT schedules_correction_lookback_check,
  DROP CONSTRAINT schedules_connection_id_stream_mode_key,
  DROP COLUMN mode,
  ADD CONSTRAINT schedules_connection_id_stream_key UNIQUE (connection_id, stream);
ALTER TABLE jobs DROP COLUMN checkpoint;
