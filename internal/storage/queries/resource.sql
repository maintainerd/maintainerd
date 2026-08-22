-- name: CreateResource :one
INSERT INTO resources (
    tenant_id, project_id, provider_id, agent_id, owner_resource_id,
    mrn_service, mrn_tenant, mrn_project, mrn_resource_type, mrn_resource_path,
    kind, name, spec, metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
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
-- It also resets the retry budget (attempts/next_attempt_at): a resource parked
-- as 'failed' after exhausting its budget gets a fresh budget only when its
-- desired state actually changes — never by the failing spec being retried
-- forever on its own.
UPDATE resources
SET spec = $2, metadata = $3,
    mrn_service = $4, mrn_tenant = $5, mrn_project = $6, mrn_resource_type = $7, mrn_resource_path = $8,
    generation = generation + 1, state = 'pending',
    attempts = 0, next_attempt_at = NULL, updated_at = now()
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
-- The reconciler's work feed (read-only variant; the gateway uses ClaimAgentWork
-- which additionally stamps the lease + agent assignment). A row is fed when:
--   * its observed state lags its spec, OR it is being torn down ('deleting'),
--     OR a prior attempt failed retryably ('error');
--   * it is not parked as 'failed' (budget exhausted — only a spec change
--     re-arms it);
--   * no dispatch lease is active and any retry backoff has elapsed.
SELECT * FROM resources
WHERE (observed_generation < generation OR state IN ('deleting', 'error'))
  AND state <> 'failed'
  AND deleted_at IS NULL
  AND (leased_until IS NULL OR leased_until < now())
  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
ORDER BY updated_at ASC
LIMIT $1;

-- name: ClaimAgentWork :many
-- PullWork's atomic claim. Feed rules match ListOutOfSyncResources, scoped to
-- items already assigned to the calling agent OR unassigned (first claim is
-- sticky: agent_id is stamped and later pulls by other agents skip the row).
-- The FOR UPDATE SKIP LOCKED subselect + single UPDATE make it impossible for
-- two concurrent agents to claim the same item; the lease keeps the item out
-- of the feed until it expires or a status report releases it.
UPDATE resources
SET agent_id = sqlc.arg(agent_id),
    leased_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    updated_at = now()
WHERE resource_id IN (
    SELECT r.resource_id FROM resources r
    WHERE (r.observed_generation < r.generation OR r.state IN ('deleting', 'error'))
      AND r.state <> 'failed'
      AND r.deleted_at IS NULL
      AND (r.agent_id = sqlc.arg(agent_id) OR r.agent_id IS NULL)
      AND (r.leased_until IS NULL OR r.leased_until < now())
      AND (r.next_attempt_at IS NULL OR r.next_attempt_at <= now())
    ORDER BY r.updated_at ASC
    LIMIT sqlc.arg(max_items)
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: ApplyAgentReport :one
-- The gateway's single write path for agent status reports. The service layer
-- computes the resulting state / attempts / backoff in Go; this statement
-- applies them atomically and ALWAYS releases the dispatch lease (a report is
-- the agent's answer for the leased item). observed_generation only moves
-- forward (GREATEST) so a stale drift report can never rewind convergence
-- progress; failure paths pass 0 to leave it untouched. `finalize` stamps
-- deleted_at — the terminal step of the teardown protocol after an agent
-- reports the workload removed.
UPDATE resources
SET status = $2,
    state = $3,
    observed_generation = GREATEST(observed_generation, $4),
    attempts = $5,
    next_attempt_at = $6,
    leased_until = NULL,
    deleted_at = CASE WHEN sqlc.arg(finalize)::bool THEN now() ELSE deleted_at END,
    updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: MarkResourceDeleting :exec
-- Deletion is a desired-state change, not an immediate erase: the row flips to
-- state='deleting' but keeps deleted_at NULL so it stays IN the work feed and
-- PullWork ships it to its agent as a teardown envelope. Only the agent's
-- "removed" report finalizes the delete (ApplyAgentReport with finalize) —
-- otherwise the workload would keep running on the host with no record of it.
UPDATE resources SET state = 'deleting', updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL;
