-- +goose Up
-- When the lab says the scans will be ready. Optional; informational until reminders exist.
ALTER TABLE processing ADD COLUMN scans_expected_at date;
-- Same idea for develop-only jobs, where negatives are the result.
ALTER TABLE processing ADD COLUMN negatives_expected_at date;

-- +goose Down
ALTER TABLE processing DROP COLUMN negatives_expected_at;
ALTER TABLE processing DROP COLUMN scans_expected_at;
