-- name: CreateAgent :one
INSERT INTO agents (tenant_id, name, status, endpoint, version, capabilities, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetAgentByID :one
SELECT * FROM agents WHERE agent_id = $1 AND deleted_at IS NULL;

-- name: GetAgentByUUID :one
SELECT * FROM agents WHERE agent_uuid = $1 AND deleted_at IS NULL;

-- name: ListAgentsByTenant :many
SELECT * FROM agents
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountAgentsByTenant :one
SELECT count(*) FROM agents WHERE tenant_id = $1 AND deleted_at IS NULL;

-- name: UpdateAgentStatus :one
UPDATE agents
SET status = $2, endpoint = $3, version = $4, capabilities = $5, updated_at = now()
WHERE agent_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: AgentHeartbeat :one
UPDATE agents
SET last_seen_at = now(), status = 'online', updated_at = now()
WHERE agent_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAgent :exec
UPDATE agents SET deleted_at = now(), updated_at = now()
WHERE agent_uuid = $1 AND deleted_at IS NULL;
