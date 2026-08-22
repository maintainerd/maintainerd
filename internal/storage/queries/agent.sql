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
-- The liveness write, and also the RECOVERY path: it stamps last_seen_at AND
-- forces status back to 'online', so an agent the sweeper marked 'offline'
-- returns to online on its very next beat with no extra reconciliation. The two
-- writes belong in one statement precisely so "seen" and "online" can never
-- disagree.
UPDATE agents
SET last_seen_at = now(), status = 'online', updated_at = now()
WHERE agent_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: MarkStaleAgentsOffline :many
-- The liveness sweeper. An agent that stopped beating is a host that may be
-- gone, and until something writes that down every agent looks online forever
-- (last_seen_at was previously written and read by nothing).
--
-- Scope is deliberately narrow:
--   * status <> 'offline' — only TRANSITIONS are returned, which is what makes
--     the caller's "emit one escalation per episode" free instead of requiring
--     de-duplication state.
--   * last_seen_at IS NOT NULL — an agent that has never checked in was never
--     online, so calling it 'offline' would overwrite the more precise truth
--     ('pending': created but not yet enrolled/registered).
UPDATE agents
SET status = 'offline', updated_at = now()
WHERE deleted_at IS NULL
  AND status <> 'offline'
  AND last_seen_at IS NOT NULL
  AND last_seen_at < now() - make_interval(secs => sqlc.arg(stale_seconds)::float8)
RETURNING *;

-- name: ListOfflineAgents :many
-- Every agent CURRENTLY considered offline, not just the ones this sweep
-- transitioned. Supervision needs the standing set: a workload assigned to a
-- host that died three ticks ago is still stranded, and a resource created or
-- reassigned after the transition would otherwise never be noticed.
SELECT * FROM agents
WHERE status = 'offline' AND deleted_at IS NULL
ORDER BY last_seen_at ASC NULLS LAST;

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
