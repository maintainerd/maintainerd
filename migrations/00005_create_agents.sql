-- +goose Up
-- An agent is the on-host executor that pulls work from Core over mTLS/gRPC and
-- runs it against the already-installed local runtime (Docker/Kubernetes). Core
-- never reaches into a host directly; it hands work to the agent.
CREATE TABLE IF NOT EXISTS agents (
    agent_id     BIGSERIAL PRIMARY KEY,
    agent_uuid   UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id    BIGINT NOT NULL REFERENCES tenants (tenant_id) ON DELETE CASCADE,
    name         VARCHAR(100) NOT NULL,
    status       VARCHAR(20) NOT NULL DEFAULT 'pending',
    endpoint     TEXT NOT NULL DEFAULT '',
    version      VARCHAR(50) NOT NULL DEFAULT '',
    capabilities JSONB NOT NULL DEFAULT '[]',
    last_seen_at TIMESTAMPTZ,
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_by   BIGINT,
    updated_by   BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agents_tenant_name ON agents (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_agents_tenant_id ON agents (tenant_id);
CREATE INDEX IF NOT EXISTS idx_agents_status ON agents (status);
CREATE INDEX IF NOT EXISTS idx_agents_last_seen_at ON agents (last_seen_at);
CREATE INDEX IF NOT EXISTS idx_agents_metadata ON agents USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_agents_created_at ON agents (created_at);
CREATE INDEX IF NOT EXISTS idx_agents_deleted_at ON agents (deleted_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS agents;
