-- +goose Up
-- The declarative resource: the heart of the control plane. Every managed thing
-- (Container, Database, Bucket, ...) is a resource with a desired `spec` and an
-- observed `status`. The reconciler drives status -> spec, idempotently.
--   generation           bumps on every spec change (the desired revision)
--   observed_generation  the generation the reconciler last acted on
--   state                coarse lifecycle for quick filtering
--   owner_resource_id    the owner graph (cascade + dependency ordering)
CREATE TABLE IF NOT EXISTS resources (
    resource_id         BIGSERIAL PRIMARY KEY,
    resource_uuid       UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id           BIGINT NOT NULL REFERENCES tenants (tenant_id) ON DELETE CASCADE,
    project_id          BIGINT NOT NULL REFERENCES projects (project_id) ON DELETE CASCADE,
    provider_id         BIGINT REFERENCES providers (provider_id) ON DELETE SET NULL,
    agent_id            BIGINT REFERENCES agents (agent_id) ON DELETE SET NULL,
    owner_resource_id   BIGINT REFERENCES resources (resource_id) ON DELETE CASCADE,
    -- Parsed MRN components. The mrn: string is presentation-only; policy and
    -- lookup code use these columns so matching stays segment-aware.
    mrn_service         VARCHAR(63) NOT NULL DEFAULT 'core',
    mrn_tenant          VARCHAR(63) NOT NULL DEFAULT '',
    mrn_project         VARCHAR(63) NOT NULL DEFAULT '',
    mrn_resource_type   VARCHAR(63) NOT NULL DEFAULT '',
    mrn_resource_path   TEXT NOT NULL DEFAULT '',
    kind                VARCHAR(50) NOT NULL,
    name                VARCHAR(100) NOT NULL,
    state               VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- Reported health, promoted out of the status JSON into its own queryable
    -- column because "running" is NOT "working": a workload whose process is up
    -- while its healthcheck fails is precisely the case supervision must act on,
    -- and a value buried in JSONB can be neither filtered nor indexed. Values
    -- mirror the runtime contract (kit runtime.HealthState): '' (nothing
    -- reported yet), 'none' (no healthcheck configured), 'starting', 'healthy',
    -- 'unhealthy'.
    health              VARCHAR(20) NOT NULL DEFAULT '',
    spec                JSONB NOT NULL DEFAULT '{}',
    status              JSONB NOT NULL DEFAULT '{}',
    generation          BIGINT NOT NULL DEFAULT 1,
    observed_generation BIGINT NOT NULL DEFAULT 0,
    -- Dispatch lease: PullWork stamps leased_until when it hands the row to an
    -- agent, so the same item is not re-dispatched (to this or another agent)
    -- until the lease expires or a status report releases it. This is what makes
    -- work delivery at-most-once-at-a-time without a proto change.
    leased_until        TIMESTAMPTZ,
    -- Retry budget: failed convergence attempts back off exponentially
    -- (next_attempt_at) instead of hot-looping the agent against a broken spec;
    -- once attempts exhausts the budget the row parks as state='failed' until a
    -- spec change resets it. Fail-closed: a poisoned spec cannot starve the feed.
    attempts            INT NOT NULL DEFAULT 0,
    next_attempt_at     TIMESTAMPTZ,
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_by          BIGINT,
    updated_by          BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_resources_project_kind_name ON resources (project_id, kind, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_resources_tenant_id ON resources (tenant_id);
CREATE INDEX IF NOT EXISTS idx_resources_project_id ON resources (project_id);
CREATE INDEX IF NOT EXISTS idx_resources_provider_id ON resources (provider_id);
CREATE INDEX IF NOT EXISTS idx_resources_agent_id ON resources (agent_id);
CREATE INDEX IF NOT EXISTS idx_resources_owner_resource_id ON resources (owner_resource_id);
CREATE INDEX IF NOT EXISTS idx_resources_mrn ON resources (mrn_service, mrn_tenant, mrn_project, mrn_resource_type, mrn_resource_path);
CREATE INDEX IF NOT EXISTS idx_resources_kind ON resources (kind);
CREATE INDEX IF NOT EXISTS idx_resources_state ON resources (state);
-- The reconciler's hot query: rows whose observed state is behind their desired
-- spec, plus teardown ('deleting') and retryable-failure ('error') rows — the
-- lease/backoff time gates are volatile and are applied by the query itself.
CREATE INDEX IF NOT EXISTS idx_resources_out_of_sync ON resources (state)
    WHERE (observed_generation < generation OR state IN ('deleting', 'error')) AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_resources_spec ON resources USING GIN (spec);
CREATE INDEX IF NOT EXISTS idx_resources_metadata ON resources USING GIN (metadata);
-- The supervisor's hot query: every system-tier instance, regardless of tenant
-- or project. Availability tier is a REGISTRATION property stored in metadata
-- (12-supervision-and-availability.md), so the keep-alive loop looks the tier up
-- rather than inferring it from kind or name.
CREATE INDEX IF NOT EXISTS idx_resources_tier ON resources ((metadata ->> 'tier')) WHERE deleted_at IS NULL;
-- Supervision reads health next to tier; unhealthy-but-running is a first-class
-- trigger, not a red dot on a dashboard.
CREATE INDEX IF NOT EXISTS idx_resources_health ON resources (health) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_resources_created_at ON resources (created_at);
CREATE INDEX IF NOT EXISTS idx_resources_deleted_at ON resources (deleted_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS resources;
