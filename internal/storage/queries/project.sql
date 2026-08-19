-- name: CreateProject :one
INSERT INTO projects (tenant_id, name, display_name, description, status, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetProjectByID :one
SELECT * FROM projects WHERE project_id = $1 AND deleted_at IS NULL;

-- name: GetProjectByUUID :one
SELECT * FROM projects WHERE project_uuid = $1 AND deleted_at IS NULL;

-- name: ListProjectsByTenant :many
SELECT * FROM projects
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountProjectsByTenant :one
SELECT count(*) FROM projects WHERE tenant_id = $1 AND deleted_at IS NULL;

-- name: UpdateProject :one
UPDATE projects
SET display_name = $2, description = $3, status = $4, metadata = $5, updated_at = now()
WHERE project_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProject :exec
UPDATE projects SET deleted_at = now(), updated_at = now()
WHERE project_uuid = $1 AND deleted_at IS NULL;
