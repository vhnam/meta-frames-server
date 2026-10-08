-- +goose Up
-- Sign-in with Google (authboss oauth2). An account has a password, a Google identity, or both.
-- OAuth2 access and refresh tokens are not stored: the API never calls Google after sign-in.
ALTER TABLE app_user
  ALTER COLUMN password_hash DROP NOT NULL,
  ADD COLUMN oauth2_provider text,
  ADD COLUMN oauth2_uid      text,
  ADD CONSTRAINT app_user_oauth2_identity UNIQUE (oauth2_provider, oauth2_uid),
  ADD CONSTRAINT app_user_oauth2_pair CHECK ((oauth2_provider IS NULL) = (oauth2_uid IS NULL)),
  ADD CONSTRAINT app_user_can_sign_in CHECK (password_hash IS NOT NULL OR oauth2_uid IS NOT NULL);

-- +goose Down
DELETE FROM app_user WHERE password_hash IS NULL;
ALTER TABLE app_user
  DROP CONSTRAINT app_user_can_sign_in,
  DROP CONSTRAINT app_user_oauth2_pair,
  DROP CONSTRAINT app_user_oauth2_identity,
  DROP COLUMN oauth2_uid,
  DROP COLUMN oauth2_provider,
  ALTER COLUMN password_hash SET NOT NULL;
