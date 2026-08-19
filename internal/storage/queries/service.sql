-- name: CreateService :one
INSERT INTO services (tenant_id, name, kind, status, endpoint, version, metadata, registered_at, is_system)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListSystemServices :many
-- The platform's system services — the ones Core must keep running at all times.
SELECT * FROM services
WHERE is_system = TRUE AND deleted_at IS NULL
ORDER BY kind ASC;

-- name: GetServiceByID :one
SELECT * FROM services
WHERE service_id = $1 AND deleted_at IS NULL;

-- name: GetServiceByUUID :one
SELECT * FROM services
WHERE service_uuid = $1 AND deleted_at IS NULL;

-- name: GetServiceByTenantAndName :one
SELECT * FROM services
WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL;

-- name: ListServicesByTenant :many
SELECT * FROM services
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountServicesByTenant :one
SELECT count(*) FROM services
WHERE tenant_id = $1 AND deleted_at IS NULL;

-- name: UpdateServiceStatus :one
UPDATE services
SET status = $2,
    endpoint = $3,
    registered_at = $4,
    updated_at = now()
WHERE service_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteService :exec
UPDATE services
SET deleted_at = now(), updated_at = now()
WHERE service_uuid = $1 AND deleted_at IS NULL;
