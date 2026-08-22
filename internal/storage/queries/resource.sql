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
-- `health` is promoted out of the reported status JSON into its own column so
-- supervision can act on unhealthy-but-running without parsing JSONB: "running"
-- is not "working" (see 00006_create_resources.sql).
UPDATE resources
SET status = $2,
    state = $3,
    observed_generation = GREATEST(observed_generation, $4),
    attempts = $5,
    next_attempt_at = $6,
    health = sqlc.arg(health),
    leased_until = NULL,
    deleted_at = CASE WHEN sqlc.arg(finalize)::bool THEN now() ELSE deleted_at END,
    updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
RETURNING *;

-- name: ListSystemTierResources :many
-- Every system-tier instance, across all tenants and projects — the supervisor's
-- feed. Availability tier is a REGISTRATION property carried in metadata
-- (12-supervision-and-availability.md), never inferred from kind or name: the
-- same image is a must-never-go-down platform component when registered as
-- system and a disposable tenant workload when it is not.
--
-- Unlike the reconciler feed this applies NO lease/backoff/state gates: the
-- supervisor's whole job is to look at rows the feed has given up on (parked
-- 'failed', leased to a dead agent) and decide they must run anyway.
SELECT * FROM resources
WHERE metadata ->> 'tier' = 'system' AND deleted_at IS NULL
ORDER BY name ASC;

-- name: RedispatchSystemResource :one
-- Force a system-tier instance back onto the work feed.
--
-- Bumping generation is what re-arms the EXISTING feed (observed_generation now
-- lags again) rather than inventing a second dispatch path; clearing
-- leased_until releases a lease held by an agent that will never answer; and
-- resetting attempts/next_attempt_at is the load-bearing part: the retry budget
-- exists to stop a poisoned TENANT spec from hot-looping an agent, but a system
-- service may never be parked as terminally 'failed' — "never goes down"
-- outranks "stop wasting cycles". System-tier work is therefore retried forever,
-- with the supervisor's interval as the backoff and an escalation record once a
-- human is needed.
--
-- The state guard is not optional: 'deleting' is an operator-requested teardown,
-- a DESIRED state, and resurrecting it would turn keep-alive into a service that
-- cannot be removed. A COMPLETED teardown needs no guard — it stamps deleted_at,
-- which the WHERE clause already excludes. State 'removed' with deleted_at still
-- NULL therefore means the workload vanished without anyone asking, which is a
-- re-dispatch case rather than an exempt one.
UPDATE resources
SET generation = generation + 1,
    state = 'pending',
    leased_until = NULL,
    attempts = 0,
    next_attempt_at = NULL,
    updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
  AND state <> 'deleting'
RETURNING *;

-- name: FlagResourceHostUnreachable :one
-- Record that the agent owning this resource has gone offline. It writes a
-- condition and health only — agent_id is deliberately UNTOUCHED: in docker mode
-- there is no scheduler to reschedule onto and the workload's data may be
-- host-local, so silently reassigning it would risk a split brain (two hosts
-- running the same stateful workload) the moment the original host came back.
-- Surface it, escalate it, let a human or an explicit policy decide.
--
-- The COALESCE guard makes it idempotent: the supervisor re-runs every interval,
-- and re-stamping would churn updated_at (which orders the work feed) and defeat
-- once-per-episode escalation. No matching row means "already flagged".
-- The flag clears on its own, because the next real agent report REPLACES status
-- wholesale (see ApplyAgentReport) — recovery needs no separate unflag path.
UPDATE resources
SET status = status || jsonb_build_object('host_unreachable', true, 'host_unreachable_at', to_jsonb(now())),
    health = 'unknown',
    updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL
  AND COALESCE(status ->> 'host_unreachable', '') <> 'true'
RETURNING *;

-- name: MarkResourceDeleting :exec
-- Deletion is a desired-state change, not an immediate erase: the row flips to
-- state='deleting' but keeps deleted_at NULL so it stays IN the work feed and
-- PullWork ships it to its agent as a teardown envelope. Only the agent's
-- "removed" report finalizes the delete (ApplyAgentReport with finalize) —
-- otherwise the workload would keep running on the host with no record of it.
UPDATE resources SET state = 'deleting', updated_at = now()
WHERE resource_uuid = $1 AND deleted_at IS NULL;
