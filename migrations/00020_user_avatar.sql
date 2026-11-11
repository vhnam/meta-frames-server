-- +goose Up
-- The profile picture of the linked Google account, refreshed at each Google sign-in.
ALTER TABLE app_user ADD COLUMN avatar_url text;

-- +goose Down
ALTER TABLE app_user DROP COLUMN avatar_url;
