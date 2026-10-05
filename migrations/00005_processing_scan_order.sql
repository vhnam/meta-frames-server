-- +goose Up
-- Which scanners were ordered for a job, and whether each runs hi-res.
-- Rows are replaced as a set by PUT /rolls/{id}/processing/{jobId}; removals are real deletes
-- (the audit trigger keeps the history).
CREATE TABLE processing_scan_order (
  processing_id uuid        NOT NULL REFERENCES processing(id),
  scanner       text        NOT NULL CHECK (scanner IN ('noritsu', 'frontier', 'other')),
  hi_res        boolean     NOT NULL DEFAULT false,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (processing_id, scanner)
);

-- Backfill: every scanner that already has live scans, plus Noritsu for scan jobs with none.
INSERT INTO processing_scan_order (processing_id, scanner)
SELECT DISTINCT processing_id, scanner FROM scan WHERE deleted_at IS NULL;

INSERT INTO processing_scan_order (processing_id, scanner)
SELECT p.id, 'noritsu' FROM processing p
WHERE p.type IN ('develop_scan', 'scan') AND p.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM processing_scan_order o WHERE o.processing_id = p.id);

CREATE TRIGGER processing_scan_order_set_updated_at BEFORE UPDATE ON processing_scan_order FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER processing_scan_order_audit AFTER INSERT OR UPDATE OR DELETE ON processing_scan_order FOR EACH ROW EXECUTE FUNCTION audit_row('processing_id');

-- +goose Down
DROP TRIGGER processing_scan_order_audit ON processing_scan_order;
DROP TRIGGER processing_scan_order_set_updated_at ON processing_scan_order;
DROP TABLE processing_scan_order;
