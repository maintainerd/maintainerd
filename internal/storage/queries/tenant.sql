-- name: CreateTenant :one
INSERT INTO tenants (name, display_name, status, is_system, auth_tenant_uuid, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetTenantByID :one
SELECT * FROM tenants
WHERE tenant_id = $1 AND deleted_at IS NULL;

-- name: GetTenantByUUID :one
SELECT * FROM tenants
WHERE tenant_uuid = $1 AND deleted_at IS NULL;

-- name: GetTenantByName :one
SELECT * FROM tenants
WHERE name = $1 AND deleted_at IS NULL;

-- name: ListTenants :many
SELECT * FROM tenants
WHERE deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountTenants :one
SELECT count(*) FROM tenants
WHERE deleted_at IS NULL;

-- name: UpdateTenant :one
UPDATE tenants
SET display_name = $2,
    status = $3,
    auth_tenant_uuid = $4,
    metadata = $5,
    updated_at = now()
WHERE tenant_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTenant :exec
UPDATE tenants
SET deleted_at = now(), updated_at = now()
WHERE tenant_uuid = $1 AND deleted_at IS NULL;
