-- name: CreateAgent :one
INSERT INTO agents (tenant_id, name, status, endpoint, version, capabilities, metadata, join_token_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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

-- name: BindAgentSubject :one
-- First authenticated Register wins: binds the verified token subject to the
-- agent row. A Register presenting a DIFFERENT subject for an already-bound
-- agent matches no rows, which the service surfaces as PermissionDenied — an
-- enrolled agent identity can never be silently taken over by another
-- principal that merely learned the agent's UUID.
UPDATE agents
SET bound_subject = $2, updated_at = now()
WHERE agent_uuid = $1 AND deleted_at IS NULL
  AND (bound_subject = '' OR bound_subject = $2)
RETURNING *;

-- name: MarkAgentEnrolled :one
UPDATE agents
SET join_token_used_at = now(), client_cert_pem = $2, updated_at = now()
WHERE agent_uuid = $1 AND deleted_at IS NULL AND join_token_used_at IS NULL
RETURNING *;
