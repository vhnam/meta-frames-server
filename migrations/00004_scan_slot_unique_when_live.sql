-- +goose Up
-- A soft-deleted scan must not block importing a new one into the same slot.
ALTER TABLE scan DROP CONSTRAINT scan_processing_id_frame_id_scanner_key;
CREATE UNIQUE INDEX scan_slot_live_idx ON scan (processing_id, frame_id, scanner) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX scan_slot_live_idx;
ALTER TABLE scan ADD CONSTRAINT scan_processing_id_frame_id_scanner_key UNIQUE (processing_id, frame_id, scanner);
