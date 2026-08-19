-- +goose Up
-- A provider binds a resource kind to a concrete driver for a tenant
-- (e.g. kind=Database -> driver=container, or driver=awsRDS). The reconciler
-- selects a provider per resource; the driver does the real provisioning.
-- Secrets in `config` are NOT stored here — they live in the secret backend and
-- are referenced by key.
CREATE TABLE IF NOT EXISTS providers (
    provider_id   BIGSERIAL PRIMARY KEY,
    provider_uuid UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id     BIGINT NOT NULL REFERENCES tenants (tenant_id) ON DELETE CASCADE,
    name          VARCHAR(100) NOT NULL,
    resource_kind VARCHAR(50) NOT NULL,
    driver        VARCHAR(50) NOT NULL,
    config        JSONB NOT NULL DEFAULT '{}',
    status        VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata      JSONB NOT NULL DEFAULT '{}',
    created_by    BIGINT,
    updated_by    BIGINT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_providers_tenant_name ON providers (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_providers_tenant_id ON providers (tenant_id);
CREATE INDEX IF NOT EXISTS idx_providers_resource_kind ON providers (resource_kind);
CREATE INDEX IF NOT EXISTS idx_providers_status ON providers (status);
CREATE INDEX IF NOT EXISTS idx_providers_metadata ON providers USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_providers_created_at ON providers (created_at);
CREATE INDEX IF NOT EXISTS idx_providers_deleted_at ON providers (deleted_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS providers;
