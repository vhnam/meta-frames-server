-- name: ListAuditLogs :many
SELECT * FROM audit_log
WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type))
  AND (sqlc.narg(entity_id)::uuid IS NULL OR entity_id = sqlc.narg(entity_id))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
ORDER BY id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: GetAuditLog :one
SELECT * FROM audit_log WHERE id = $1;
