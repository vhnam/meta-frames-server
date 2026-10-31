-- Built-in scanners have no owner: every account sees them next to its own and cannot change them.

-- name: ListScanners :many
SELECT * FROM scanner WHERE (owner_id IS NULL OR owner_id = $1) AND deleted_at IS NULL
ORDER BY owner_id IS NOT NULL, lower(brand), lower(model) NULLS FIRST;

-- name: GetScanner :one
SELECT * FROM scanner WHERE id = $1 AND (owner_id IS NULL OR owner_id = $2) AND deleted_at IS NULL;

-- name: GetOwnScannerNamed :one
SELECT * FROM scanner
WHERE owner_id = $1 AND lower(brand) = lower(@brand) AND lower(COALESCE(model, '')) = lower(COALESCE(sqlc.narg(model)::text, ''))
  AND deleted_at IS NULL;

-- name: BuiltInScannerNamed :one
SELECT EXISTS (
  SELECT 1 FROM scanner
  WHERE owner_id IS NULL AND lower(brand) = lower(@brand) AND lower(COALESCE(model, '')) = lower(COALESCE(sqlc.narg(model)::text, ''))
    AND deleted_at IS NULL
);

-- name: InsertScanner :one
INSERT INTO scanner (id, brand, model, kind, notes, default_settings, owner_id) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateScanner :one
UPDATE scanner SET brand = $2, model = $3, kind = $4, notes = $5, default_settings = $6
WHERE id = $1 AND owner_id = $7 AND deleted_at IS NULL RETURNING *;

-- name: SoftDeleteScanner :execrows
UPDATE scanner SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- Deleted scans count too: they can be restored and still name their scanner.
-- name: ScannerInUse :one
SELECT EXISTS (
  SELECT 1 FROM processing_scan_order o WHERE o.scanner_id = @scanner_id::uuid
  UNION ALL
  SELECT 1 FROM scan s WHERE s.scanner_id = @scanner_id::uuid
);

-- Scanners of the account's labs, each lab's in the order they were given.
-- name: ListLabScanners :many
SELECT ls.lab_id, ls.scanner_id FROM lab_scanner ls JOIN lab l ON l.id = ls.lab_id
WHERE l.owner_id = $1 ORDER BY ls.lab_id, ls.position;

-- name: DeleteLabScannersExcept :exec
DELETE FROM lab_scanner WHERE lab_id = $1 AND NOT (scanner_id = ANY(@keep::uuid[]));

-- name: UpsertLabScanner :exec
INSERT INTO lab_scanner (lab_id, scanner_id, position) VALUES ($1, $2, $3)
ON CONFLICT (lab_id, scanner_id) DO UPDATE SET position = EXCLUDED.position
WHERE lab_scanner.position IS DISTINCT FROM EXCLUDED.position;

-- A deleted scanner leaves the labs that ran it.
-- name: RemoveScannerFromLabs :exec
DELETE FROM lab_scanner WHERE scanner_id = $1;
