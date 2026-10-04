-- Sleep stages gain unknown (no data: neither asleep nor awake), restless and out_of_bed (J25.1;
-- docs/architecture/data-model.md#tables). Only asleep stages count as sleep; in-bed time is the session span.

-- +goose Up
ALTER TABLE sleep_stages DROP CONSTRAINT sleep_stages_stage_check;
ALTER TABLE sleep_stages ADD CONSTRAINT sleep_stages_stage_check
  CHECK (stage IN ('awake', 'light', 'deep', 'rem', 'asleep_unspecified', 'in_bed', 'unknown', 'restless', 'out_of_bed'));

-- +goose Down
DELETE FROM sleep_stages WHERE stage IN ('unknown', 'restless', 'out_of_bed');
ALTER TABLE sleep_stages DROP CONSTRAINT sleep_stages_stage_check;
ALTER TABLE sleep_stages ADD CONSTRAINT sleep_stages_stage_check
  CHECK (stage IN ('awake', 'light', 'deep', 'rem', 'asleep_unspecified', 'in_bed'));
