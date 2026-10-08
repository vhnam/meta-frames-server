-- name: GetLens :one
SELECT * FROM lens WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: GetLensForUpdate :one
SELECT * FROM lens WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE;

-- name: GetLensesByIDs :many
SELECT * FROM lens WHERE id = ANY(sqlc.arg(ids)::uuid[]) AND owner_id = sqlc.arg(owner_id) AND deleted_at IS NULL;

-- name: ListLenses :many
SELECT * FROM lens
WHERE owner_id = @owner_id AND deleted_at IS NULL AND (NOT @active_only::bool OR is_active)
ORDER BY (sqlc.narg(prefer_mount)::text IS NOT NULL AND mount = sqlc.narg(prefer_mount)::text) DESC,
         focal_length, brand, model;

-- name: InsertLens :one
INSERT INTO lens (id, brand, model, mount, description, focal_length, max_aperture, is_built_in, owner_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateLens :one
UPDATE lens SET brand = $2, model = $3, mount = $4, description = $5,
                focal_length = $6, max_aperture = $7
WHERE id = $1 AND owner_id = $8 AND deleted_at IS NULL
RETURNING *;

-- name: SetLensActive :one
UPDATE lens SET is_active = $2 WHERE id = $1 AND owner_id = $3 AND deleted_at IS NULL RETURNING *;

-- The lens is checked to belong to the account first.
-- name: LensIsOnRolls :one
SELECT EXISTS (SELECT 1 FROM roll_lens WHERE lens_id = $1);

-- name: SoftDeleteLens :execrows
UPDATE lens SET deleted_at = now() WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;
