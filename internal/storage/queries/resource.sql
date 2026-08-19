-- name: CreateResource :one
INSERT INTO resources (tenant_id, project_id, provider_id, agent_id, owner_resource_id, kind, name, spec, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetResourceByID :one
SELECT * FROM resources WHERE resource_id = $1 AND deleted_at IS NULL;

-- name: GetResourceByUUID :one
SELECT * FROM resources WHERE resource_uuid = $1 AND deleted_at IS NULL;

-- name: ListResourcesByProject :many
SELECT * FROM resources
WHERE project_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountResourcesByProject :one
SELECT count(*) FROM resources WHERE project_id = $1 AND deleted_at IS NULL;

-- name: ListResourcesByTenant :many
SELECT * FROM resources
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateResourceSpec :one
-- A spec change bumps generation and re-arms the reconciler (state -> pending).
UPDATE resources
SET spec = $2, metadata = $3, generation = generation + 1, state = 'pending', updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateResourceStatus :one
-- The reconciler writes observed state back, marking how far it has caught up.
UPDATE resources
SET status = $2, state = $3, observed_generation = $4, updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: AssignResourceAgent :one
UPDATE resources
SET agent_id = $2, updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListOutOfSyncResources :many
-- The reconciler's work feed: resources whose observed state lags their spec.
SELECT * FROM resources
WHERE observed_generation < generation AND state <> 'failed' AND deleted_at IS NULL
ORDER BY updated_at ASC
LIMIT $1;

-- name: SoftDeleteResource :exec
-- Deletion is a desired-state change too: mark deleting so the reconciler can tear down.
UPDATE resources SET state = 'deleting', deleted_at = now(), updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL;
