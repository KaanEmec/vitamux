-- App sessions (J22.2): the iOS app signs in like the panel and gets a bearer token instead of a
-- cookie. Same table, so Settings › Security lists and ends both; existing rows are browser ones.

-- +goose Up
ALTER TABLE sessions
  ADD COLUMN kind text NOT NULL DEFAULT 'browser' CHECK (kind IN ('browser', 'app')),
  ADD COLUMN name text CHECK (char_length(name) BETWEEN 1 AND 100),
  ADD CONSTRAINT sessions_app_named CHECK ((kind = 'app') = (name IS NOT NULL));
COMMENT ON TABLE sessions IS 'Owner sessions: browser (cookie token) or app (bearer vmx_ses_ token, named after the device). The row keeps only a SHA-256.';
COMMENT ON COLUMN sessions.name IS 'Device name the app sent at sign-in; NULL for browser sessions.';

-- +goose Down
COMMENT ON TABLE sessions IS 'Server-side UI sessions; the cookie holds the token, the row only its SHA-256.';
ALTER TABLE sessions DROP CONSTRAINT sessions_app_named, DROP COLUMN name, DROP COLUMN kind;
