-- Owner auth (J03.2): TOTP enrolment state, replay guard, recovery codes.

-- +goose Up
ALTER TABLE users
  ADD COLUMN totp_enabled_at timestamptz,
  ADD COLUMN totp_last_step  bigint,
  ADD CONSTRAINT users_totp_enabled_check CHECK (totp_enabled_at IS NULL OR totp_ciphertext IS NOT NULL);
COMMENT ON COLUMN users.totp_enabled_at IS 'Set when enrolment is confirmed; a sealed secret without it is a pending enrolment.';
COMMENT ON COLUMN users.totp_last_step IS 'Last accepted TOTP time step; a code is accepted only for a later step (no replay).';

CREATE TABLE recovery_codes (
  user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  code_hash  bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  used_at    timestamptz,
  PRIMARY KEY (user_id, code_hash)
);
COMMENT ON TABLE recovery_codes IS 'Single-use TOTP recovery codes; only the SHA-256 of each 80-bit code is stored.';

-- +goose Down
DROP TABLE recovery_codes;
ALTER TABLE users
  DROP CONSTRAINT users_totp_enabled_check,
  DROP COLUMN totp_last_step,
  DROP COLUMN totp_enabled_at;
