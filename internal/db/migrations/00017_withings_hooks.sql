-- Withings notification hooks (J08.4); see docs/providers/withings.md#notifications.

-- +goose Up
ALTER TABLE connections ADD COLUMN hook_token_hash bytea UNIQUE CHECK (length(hook_token_hash) = 32);
COMMENT ON COLUMN connections.hook_token_hash IS 'SHA-256 of the random token in the notification callback URL /webhooks/<provider>/<token>; null while not subscribed. The token itself is only sent to the provider.';

-- +goose Down
ALTER TABLE connections DROP COLUMN hook_token_hash;
