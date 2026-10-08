-- +goose Up
-- Login throttling for the authboss lock module.
ALTER TABLE app_user
  ADD COLUMN attempt_count integer NOT NULL DEFAULT 0,
  ADD COLUMN last_attempt  timestamptz,
  ADD COLUMN locked_until  timestamptz;

-- Server-side sessions, so logout and password resets can end them. The cookie holds a random
-- token; only its SHA-256 is stored.
CREATE TABLE user_session (
  token_hash bytea PRIMARY KEY,
  user_id    uuid        NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX user_session_user_id ON user_session (user_id);

-- +goose Down
DROP TABLE user_session;
ALTER TABLE app_user DROP COLUMN locked_until, DROP COLUMN last_attempt, DROP COLUMN attempt_count;
