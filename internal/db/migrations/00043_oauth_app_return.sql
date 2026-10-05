-- Native OAuth return (J22.3): an authorization begun by the iOS app goes through a single-use
-- start ticket that sets the binding cookie in the app's auth browser, and its callback returns
-- to the app (docs/architecture/connectors.md#oauth-connection-flow).

-- +goose Up
ALTER TABLE oauth_states
  ADD COLUMN return_to text NOT NULL DEFAULT 'browser' CHECK (return_to IN ('browser', 'app')),
  ADD COLUMN ticket_hash bytea UNIQUE,
  ADD COLUMN start_url text,
  ADD COLUMN binding bytea,
  ADD CONSTRAINT oauth_states_ticket CHECK (ticket_hash IS NULL OR (return_to = 'app' AND start_url IS NOT NULL AND binding IS NOT NULL));
COMMENT ON COLUMN oauth_states.return_to IS 'Where the callback sends the owner: the panel (browser) or the app (vitamux://connections).';
COMMENT ON COLUMN oauth_states.ticket_hash IS 'SHA-256 of the start ticket of an app redirect step; cleared when GET /oauth/{provider}/start uses it (single use).';
COMMENT ON COLUMN oauth_states.start_url IS 'Provider URL the start route redirects to.';
COMMENT ON COLUMN oauth_states.binding IS 'Browser binding the start route sets as cookie, sealed by internal/crypto (purpose credentials, AAD auth-binding:<id>). Rows live minutes, so key rotation skips them.';

-- +goose Down
ALTER TABLE oauth_states DROP CONSTRAINT oauth_states_ticket,
  DROP COLUMN binding, DROP COLUMN start_url, DROP COLUMN ticket_hash, DROP COLUMN return_to;
