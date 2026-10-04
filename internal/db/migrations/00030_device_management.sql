-- Device management (J20.7; docs/architecture/api.md#source-devices): the owner names a device, sets
-- its type and merges duplicates of one physical device. A type the owner set wins over the
-- normalizer's. A merged device keeps its row, so its fingerprint still resolves; the canonical
-- writer follows merged_into and the merge repoints its records.
-- The composite key keeps a merge inside one owner and provider; the merge handler refuses a
-- target that is itself merged, so merged_into is never a chain.

-- +goose Up
ALTER TABLE devices
  ADD COLUMN name text CHECK (length(name) BETWEEN 1 AND 100),
  ADD COLUMN device_type_by_owner boolean NOT NULL DEFAULT false,
  ADD COLUMN merged_into uuid CHECK (merged_into <> id),
  ADD CONSTRAINT devices_id_owner_key UNIQUE (id, user_id, provider_id),
  ADD CONSTRAINT devices_merged_into_fkey FOREIGN KEY (merged_into, user_id, provider_id)
    REFERENCES devices (id, user_id, provider_id);
CREATE INDEX devices_merged_into_idx ON devices (merged_into) WHERE merged_into IS NOT NULL;
COMMENT ON COLUMN devices.name IS 'The owner''s label; null shows the model or fingerprint.';
COMMENT ON COLUMN devices.device_type_by_owner IS 'The owner set device_type; normalizers no longer change it.';
COMMENT ON COLUMN devices.merged_into IS 'The device this one was merged into (same owner and provider, never itself merged). Records of this fingerprint are written to that device.';

-- +goose Down
DROP INDEX devices_merged_into_idx;
ALTER TABLE devices
  DROP CONSTRAINT devices_merged_into_fkey,
  DROP CONSTRAINT devices_id_owner_key,
  DROP COLUMN merged_into,
  DROP COLUMN device_type_by_owner,
  DROP COLUMN name;
