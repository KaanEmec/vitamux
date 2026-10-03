-- Pending OAuth authorizations (J08.2): the server half of the signed, single-use, session-bound
-- `state` (docs/architecture/connectors.md#oauth-connection-flow).

-- +goose Up
CREATE TABLE oauth_states (
  id            uuid PRIMARY KEY,
  user_id       uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  session_id    uuid NOT NULL REFERENCES sessions ON DELETE CASCADE,
  provider_id   smallint NOT NULL REFERENCES providers,
  connection_id uuid REFERENCES connections ON DELETE CASCADE,
  expires_at    timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE oauth_states IS 'One row per started authorization; the callback deletes it (single use). Logging out deletes it with the session.';
COMMENT ON COLUMN oauth_states.connection_id IS 'Connection being reauthorized; null for a new connection or a reconnect recognised by account.';

-- +goose Down
DROP TABLE oauth_states;
