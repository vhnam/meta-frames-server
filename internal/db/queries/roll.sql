-- name: GetRoll :one
SELECT * FROM roll WHERE id = $1 AND deleted_at IS NULL;

-- name: GetRollForUpdate :one
SELECT * FROM roll WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: InsertRoll :one
INSERT INTO roll (id, film_stock_id, format, exposures, price, expiry_year, expiry_month)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateRoll :one
UPDATE roll SET film_stock_id = $2, format = $3, exposures = $4, price = $5,
       expiry_year = $6, expiry_month = $7, shot_iso = $8, started_at = $9,
       finished_at = $10, description = $11
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: LoadRoll :one
UPDATE roll SET camera_id = $2, status = 'in_camera', started_at = $3, shot_iso = $4
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: FinishRoll :one
UPDATE roll SET status = 'done_shooting', finished_at = $2 WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: SetRollStatus :exec
UPDATE roll SET status = $2 WHERE id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteRoll :execrows
UPDATE roll SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: ListRollSummaries :many
SELECT r.*, fs.brand AS stock_brand, fs.name AS stock_name, fs.box_iso,
       c.brand AS camera_brand, c.model AS camera_model,
       EXISTS (SELECT 1 FROM processing p
               WHERE p.roll_id = r.id AND p.deleted_at IS NULL AND p.lab_id IS NOT NULL AND p.negatives_returned_at IS NULL) AS negatives_at_lab
FROM roll r
JOIN film_stock fs ON fs.id = r.film_stock_id
LEFT JOIN camera c ON c.id = r.camera_id
WHERE r.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status))
  AND (sqlc.narg(film_stock_id)::uuid IS NULL OR r.film_stock_id = sqlc.narg(film_stock_id))
  AND (sqlc.narg(camera_id)::uuid IS NULL OR r.camera_id = sqlc.narg(camera_id))
  AND (sqlc.narg(lens_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM roll_lens rl WHERE rl.roll_id = r.id AND rl.lens_id = sqlc.narg(lens_id)))
  AND (sqlc.narg(format)::int IS NULL OR r.format = sqlc.narg(format))
  AND (sqlc.narg(started_from)::date IS NULL OR r.started_at >= sqlc.narg(started_from))
  AND (sqlc.narg(started_to)::date IS NULL OR r.started_at <= sqlc.narg(started_to))
  AND (sqlc.narg(roll_id)::uuid IS NULL OR r.id = sqlc.narg(roll_id))
  AND (sqlc.narg(focal_length)::int IS NULL OR EXISTS (
        SELECT 1 FROM roll_lens rl JOIN lens l ON l.id = rl.lens_id
        WHERE rl.roll_id = r.id AND l.focal_length = sqlc.narg(focal_length)))
ORDER BY r.created_at DESC;

-- name: ListRollLenses :many
SELECT l.* FROM lens l JOIN roll_lens rl ON rl.lens_id = l.id
WHERE rl.roll_id = $1 ORDER BY l.focal_length, l.brand, l.model;

-- name: InsertRollLens :exec
INSERT INTO roll_lens (roll_id, lens_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: DeleteRollLensesExcept :exec
DELETE FROM roll_lens WHERE roll_id = $1 AND NOT (lens_id = ANY($2::uuid[]));

-- name: RollHasScansReceived :one
SELECT EXISTS (SELECT 1 FROM processing WHERE roll_id = $1 AND deleted_at IS NULL AND scans_received_at IS NOT NULL);

-- name: RollHasOpenJob :one
SELECT EXISTS (
  SELECT 1 FROM processing
  WHERE roll_id = $1 AND deleted_at IS NULL
    AND ((type IN ('develop_scan', 'scan') AND scans_received_at IS NULL)
      OR (type IN ('develop', 'print') AND negatives_returned_at IS NULL))
);

-- name: ListFrames :many
SELECT * FROM frame WHERE roll_id = $1 ORDER BY number;

-- name: GetFrame :one
SELECT * FROM frame WHERE id = $1;

-- name: GetFrameByNumber :one
SELECT * FROM frame WHERE roll_id = $1 AND number = $2;

-- name: UpsertFrame :one
INSERT INTO frame (id, roll_id, number) VALUES ($1, $2, $3)
ON CONFLICT (roll_id, number) DO UPDATE SET number = EXCLUDED.number
RETURNING *;

-- name: SetFrameNotes :one
UPDATE frame SET notes = $2 WHERE id = $1 RETURNING *;

-- name: ListRollScans :many
SELECT s.*, f.number AS frame_number FROM scan s JOIN frame f ON f.id = s.frame_id
WHERE f.roll_id = $1 AND s.deleted_at IS NULL ORDER BY f.number, s.scanner;

-- name: RollSpend :one
SELECT COALESCE(r.price, 0)::int AS roll_price, (r.price IS NULL)::bool AS roll_price_missing,
       (SELECT COALESCE(sum(price), 0) FROM processing WHERE roll_id = r.id AND deleted_at IS NULL)::int AS processing_price,
       EXISTS (SELECT 1 FROM processing WHERE roll_id = r.id AND deleted_at IS NULL AND price IS NULL)::bool AS processing_price_missing
FROM roll r WHERE r.id = $1 AND r.deleted_at IS NULL;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_key WHERE key = $1;

-- name: ClaimIdempotencyKey :execrows
INSERT INTO idempotency_key (key) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: FinishIdempotencyKey :exec
UPDATE idempotency_key SET status = $2, response = $3 WHERE key = $1;
