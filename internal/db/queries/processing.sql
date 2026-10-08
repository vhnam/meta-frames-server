-- name: GetLab :one
SELECT * FROM lab WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: ListLabs :many
SELECT * FROM lab WHERE owner_id = $1 AND deleted_at IS NULL ORDER BY name;

-- name: InsertLab :one
INSERT INTO lab (id, name, address, owner_id) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: UpdateLab :one
UPDATE lab SET name = $2, address = $3 WHERE id = $1 AND owner_id = $4 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteLab :execrows
UPDATE lab SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: LabHasProcessing :one
SELECT EXISTS (SELECT 1 FROM processing WHERE lab_id = $1 AND owner_id = $2);

-- name: GetProcessing :one
SELECT * FROM processing WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: GetProcessingForUpdate :one
SELECT * FROM processing WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE;

-- name: ListRollProcessing :many
SELECT p.*, COALESCE(l.name, '') AS lab_name
FROM processing p LEFT JOIN lab l ON l.id = p.lab_id
WHERE p.roll_id = $1 AND p.owner_id = $2 AND p.deleted_at IS NULL ORDER BY p.sent_at, p.created_at;

-- name: InsertProcessing :one
INSERT INTO processing (id, roll_id, lab_id, type, process, sent_at, scans_expected_at, negatives_expected_at, price, notes, owner_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateProcessing :one
UPDATE processing SET lab_id = $2, sent_at = $3, scans_expected_at = $4, negatives_expected_at = $5, price = $6, notes = $7
WHERE id = $1 AND owner_id = $8 AND deleted_at IS NULL RETURNING *;

-- name: SetScansReceived :one
UPDATE processing SET scans_received_at = $2 WHERE id = $1 AND owner_id = $3 AND deleted_at IS NULL RETURNING *;

-- name: SetNegativesReturned :one
UPDATE processing SET negatives_returned_at = $2 WHERE id = $1 AND owner_id = $3 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteProcessing :execrows
UPDATE processing SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: ProcessingHasScans :one
SELECT EXISTS (SELECT 1 FROM scan WHERE processing_id = $1 AND owner_id = $2 AND deleted_at IS NULL);

-- name: NegativesAtLab :many
SELECT p.id, p.roll_id, p.lab_id, l.name AS lab_name, p.type, p.sent_at,
       (CURRENT_DATE - p.sent_at)::int AS days_since_sent,
       fs.brand AS stock_brand, fs.name AS stock_name
FROM processing p
JOIN lab l ON l.id = p.lab_id
JOIN roll r ON r.id = p.roll_id
JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE p.owner_id = $1 AND p.negatives_returned_at IS NULL AND p.deleted_at IS NULL AND r.deleted_at IS NULL
ORDER BY p.sent_at;

-- name: ListProcessingScans :many
SELECT s.*, f.number AS frame_number FROM scan s JOIN frame f ON f.id = s.frame_id
WHERE s.processing_id = @processing_id AND s.owner_id = @owner_id AND s.deleted_at IS NULL
  AND (sqlc.narg(scanner)::text IS NULL OR s.scanner = sqlc.narg(scanner))
ORDER BY f.number, s.scanner;

-- name: GetScan :one
SELECT * FROM scan WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: GetScanSlot :one
SELECT * FROM scan WHERE processing_id = $1 AND frame_id = $2 AND scanner = $3 AND owner_id = $4 AND deleted_at IS NULL;

-- name: InsertScan :one
INSERT INTO scan (id, processing_id, frame_id, scanner, file_key, file_name, content_type, size_bytes, owner_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ReplaceScan :one
UPDATE scan SET file_key = $2, file_name = $3, content_type = $4, size_bytes = $5, created_at = now()
WHERE id = $1 AND owner_id = $6 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteScan :execrows
UPDATE scan SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: ListFrameScans :many
SELECT * FROM scan WHERE frame_id = $1 AND owner_id = $2 AND deleted_at IS NULL ORDER BY scanner;

-- name: ListJobFrameNumbers :many
SELECT DISTINCT f.number FROM scan s JOIN frame f ON f.id = s.frame_id
WHERE s.processing_id = $1 AND s.owner_id = $2 AND s.deleted_at IS NULL ORDER BY f.number;

-- Scan orders hang off a job; the job is checked to belong to the account before these run.

-- name: ListScanOrders :many
SELECT o.processing_id, o.scanner, o.hi_res,
       (SELECT count(*) FROM scan s WHERE s.processing_id = o.processing_id AND s.scanner = o.scanner AND s.deleted_at IS NULL)::int AS scan_count
FROM processing_scan_order o
JOIN processing p ON p.id = o.processing_id
WHERE o.processing_id = ANY(sqlc.arg(processing_ids)::uuid[]) AND p.owner_id = sqlc.arg(owner_id)
ORDER BY o.processing_id, o.scanner;

-- name: UpsertScanOrder :exec
INSERT INTO processing_scan_order (processing_id, scanner, hi_res) VALUES ($1, $2, $3)
ON CONFLICT (processing_id, scanner) DO UPDATE SET hi_res = EXCLUDED.hi_res;

-- name: DeleteScanOrder :execrows
DELETE FROM processing_scan_order WHERE processing_id = $1 AND scanner = $2;

-- name: ScanOrderExists :one
SELECT EXISTS (SELECT 1 FROM processing_scan_order WHERE processing_id = $1 AND scanner = $2);

-- name: ScannerHasScans :one
SELECT EXISTS (SELECT 1 FROM scan WHERE processing_id = $1 AND scanner = $2 AND deleted_at IS NULL);
