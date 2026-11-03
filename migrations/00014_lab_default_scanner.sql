-- +goose Up
-- A lab's default scanner becomes optional: a lab can name scanners without one being the default.
-- Labs saved so far keep their first scanner as the default.
ALTER TABLE lab_scanner ADD COLUMN is_default boolean NOT NULL DEFAULT false;

ALTER TABLE lab_scanner DISABLE TRIGGER USER;
-- The lowest position, not 0: deleting a scanner leaves a gap in the lab's positions.
UPDATE lab_scanner SET is_default = true
WHERE (lab_id, position) IN (SELECT lab_id, min(position) FROM lab_scanner GROUP BY lab_id);
ALTER TABLE lab_scanner ENABLE TRIGGER USER;

CREATE UNIQUE INDEX lab_scanner_default_idx ON lab_scanner (lab_id) WHERE is_default;

-- +goose Down
DROP INDEX lab_scanner_default_idx;
ALTER TABLE lab_scanner DROP COLUMN is_default;
