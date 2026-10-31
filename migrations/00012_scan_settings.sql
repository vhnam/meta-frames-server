-- +goose Up
-- Scan options depend on the scanner: a minilab has a resolution, a flatbed a dpi, a camera rig
-- pixel shift. An order keeps its choices as settings, checked by the server against the options
-- of its scanner's kind; a scanner keeps the settings its orders start from.
ALTER TABLE processing_scan_order ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';
ALTER TABLE scanner ADD COLUMN default_settings jsonb NOT NULL DEFAULT '{}';

-- Moving hi_res into settings is not an edit: no audit entry, no new updated_at.
ALTER TABLE processing_scan_order DISABLE TRIGGER USER;
UPDATE processing_scan_order
SET settings = jsonb_build_object('resolution', CASE WHEN hi_res THEN 'hi_res' ELSE 'standard' END);
ALTER TABLE processing_scan_order ENABLE TRIGGER USER;

ALTER TABLE processing_scan_order DROP COLUMN hi_res;

-- +goose Down
ALTER TABLE processing_scan_order ADD COLUMN hi_res boolean NOT NULL DEFAULT false;
ALTER TABLE processing_scan_order DISABLE TRIGGER USER;
UPDATE processing_scan_order SET hi_res = (settings->>'resolution' = 'hi_res');
ALTER TABLE processing_scan_order ENABLE TRIGGER USER;
ALTER TABLE processing_scan_order DROP COLUMN settings;
ALTER TABLE scanner DROP COLUMN default_settings;
