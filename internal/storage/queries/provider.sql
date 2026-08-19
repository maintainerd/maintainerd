-- name: CreateProvider :one
INSERT INTO providers (tenant_id, name, resource_kind, driver, config, status, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetProviderByID :one
SELECT * FROM providers WHERE provider_id = $1 AND deleted_at IS NULL;

-- name: GetProviderByUUID :one
SELECT * FROM providers WHERE provider_uuid = $1 AND deleted_at IS NULL;

-- name: ListProvidersByTenant :many
SELECT * FROM providers
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListProvidersByKind :many
SELECT * FROM providers
WHERE tenant_id = $1 AND resource_kind = $2 AND status = 'active' AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: CountProvidersByTenant :one
SELECT count(*) FROM providers WHERE tenant_id = $1 AND deleted_at IS NULL;

-- name: UpdateProvider :one
UPDATE providers
SET driver = $2, config = $3, status = $4, metadata = $5, updated_at = now()
WHERE provider_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProvider :exec
UPDATE providers SET deleted_at = now(), updated_at = now()
WHERE provider_uuid = $1 AND deleted_at IS NULL;
