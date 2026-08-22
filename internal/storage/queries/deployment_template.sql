-- name: CreateDeploymentTemplate :one
INSERT INTO deployment_templates (name, version, capability, image, parameters, spec, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetDeploymentTemplate :one
SELECT * FROM deployment_templates
WHERE name = $1 AND version = $2 AND deleted_at IS NULL;

-- name: ListDeploymentTemplates :many
SELECT * FROM deployment_templates
WHERE deleted_at IS NULL
ORDER BY name ASC, version ASC
LIMIT $1 OFFSET $2;

-- name: CountDeploymentTemplates :one
SELECT count(*) FROM deployment_templates WHERE deleted_at IS NULL;

-- name: SoftDeleteDeploymentTemplate :exec
UPDATE deployment_templates SET deleted_at = now(), updated_at = now()
WHERE name = $1 AND version = $2 AND deleted_at IS NULL;
