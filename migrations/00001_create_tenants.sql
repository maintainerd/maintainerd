-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Core's tenant is a thin record keyed to Auth's tenant. Auth owns identity,
-- users, and memberships; Core keys its resource inventory to auth_tenant_uuid.
CREATE TABLE IF NOT EXISTS tenants (
    tenant_id        BIGSERIAL PRIMARY KEY,
    tenant_uuid      UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    auth_tenant_uuid UUID,
    name             VARCHAR(63) NOT NULL,
    display_name     VARCHAR(255) NOT NULL DEFAULT '',
    status           VARCHAR(20) NOT NULL DEFAULT 'active',
    is_system        BOOLEAN NOT NULL DEFAULT FALSE,
    metadata         JSONB NOT NULL DEFAULT '{}',
    created_by       BIGINT,
    updated_by       BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ
);

-- name is the unique, DNS-safe subdomain slug; unique among live rows.
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenants_name ON tenants (name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants (status);
CREATE INDEX IF NOT EXISTS idx_tenants_auth_tenant_uuid ON tenants (auth_tenant_uuid);
CREATE INDEX IF NOT EXISTS idx_tenants_metadata ON tenants USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_tenants_created_at ON tenants (created_at);
CREATE INDEX IF NOT EXISTS idx_tenants_deleted_at ON tenants (deleted_at) WHERE deleted_at IS NULL;
-- Singleton guarantee: at most one live system tenant (the bootstrap root).
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenants_single_system ON tenants (is_system) WHERE is_system = TRUE AND deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS tenants;
