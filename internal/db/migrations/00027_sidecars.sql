-- Remote sidecar connectors (J17.2; docs/architecture/connectors.md#remote-sidecar-mode): sealed
-- multi-step authorization continuations, the upstream package of a connection, and provider
-- registration for sidecars.

-- +goose Up
ALTER TABLE oauth_states ADD COLUMN session bytea;
COMMENT ON TABLE oauth_states IS 'One row per pending authorization step; the callback or the next continue deletes it (single use). Logging out deletes it with the session.';
COMMENT ON COLUMN oauth_states.session IS 'Opaque connector continuation (e.g. a PKCE verifier or a login session), sealed by internal/crypto (purpose credentials, AAD auth-session:<id>). Never sent to the browser; rows live minutes, so key rotation skips them.';

ALTER TABLE connections ADD COLUMN upstream jsonb;
COMMENT ON COLUMN connections.upstream IS 'Upstream package a sidecar connector wraps: {package, version, source_url}; null for in-process connectors.';

-- providers stays read-only for the app role; a sidecar's provider is added only through this.
-- +goose StatementBegin
CREATE FUNCTION register_provider(code text, name text) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = vitamux, pg_temp AS $$
  INSERT INTO providers (code, name) VALUES ($1, $2) ON CONFLICT (code) DO NOTHING
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION register_provider(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION register_provider(text, text) TO vitamux_app;

-- +goose Down
DROP FUNCTION register_provider(text, text);
ALTER TABLE connections DROP COLUMN upstream;
ALTER TABLE oauth_states DROP COLUMN session;
COMMENT ON TABLE oauth_states IS 'One row per started authorization; the callback deletes it (single use). Logging out deletes it with the session.';
