-- name: GetCamera :one
SELECT * FROM camera WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCameraForUpdate :one
SELECT * FROM camera WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ListCameras :many
SELECT * FROM camera WHERE deleted_at IS NULL ORDER BY is_active DESC, brand, model;

-- name: InsertCamera :one
INSERT INTO camera (id, brand, model, mount, description, has_fixed_lens)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateCamera :one
UPDATE camera SET brand = $2, model = $3, mount = $4, description = $5
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SetCameraActive :one
UPDATE camera SET is_active = $2 WHERE id = $1 AND deleted_at IS NULL RETURNING *;

-- name: CountCameraRolls :one
SELECT count(*) FROM roll WHERE camera_id = $1;

-- name: CameraIsLoaded :one
SELECT EXISTS (SELECT 1 FROM roll WHERE camera_id = $1 AND status = 'in_camera');

-- name: ListLoadedRolls :many
SELECT r.camera_id, r.id AS roll_id, r.shot_iso, r.started_at,
       fs.id AS stock_id, fs.brand AS stock_brand, fs.name AS stock_name, fs.box_iso,
       COALESCE(CURRENT_DATE - r.started_at, 0)::int AS days_loaded
FROM roll r
JOIN film_stock fs ON fs.id = r.film_stock_id
WHERE r.status = 'in_camera' AND r.camera_id IS NOT NULL;

-- name: InsertCameraLens :exec
INSERT INTO camera_lens (camera_id, lens_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: DeleteCameraLensesExcept :exec
DELETE FROM camera_lens WHERE camera_id = $1 AND NOT (lens_id = ANY($2::uuid[]));

-- name: ListCameraLenses :many
SELECT l.* FROM lens l JOIN camera_lens cl ON cl.lens_id = l.id
WHERE cl.camera_id = $1 AND l.deleted_at IS NULL
ORDER BY l.focal_length, l.brand, l.model;

-- name: SetBuiltInLensActive :exec
UPDATE lens SET is_active = $2
WHERE is_built_in AND id IN (SELECT lens_id FROM camera_lens WHERE camera_id = $1);

-- name: ListBuiltInLensLinks :many
SELECT cl.camera_id, cl.lens_id
FROM camera_lens cl JOIN lens l ON l.id = cl.lens_id
WHERE l.is_built_in AND l.deleted_at IS NULL;

-- name: SoftDeleteBuiltInLenses :exec
UPDATE lens SET deleted_at = now()
WHERE is_built_in AND deleted_at IS NULL
  AND id IN (SELECT lens_id FROM camera_lens WHERE camera_id = $1);

-- name: SoftDeleteCamera :execrows
UPDATE camera SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;
