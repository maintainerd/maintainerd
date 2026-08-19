-- +goose Up
-- A service is a control-plane service Core tracks/registers for a tenant (Auth,
-- Secret, Docker, Database, ...). Distinct from a resource: this is the registry
-- of platform services, not an individually reconciled resource instance.
CREATE TABLE IF NOT EXISTS services (
    service_id    BIGSERIAL PRIMARY KEY,
    service_uuid  UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id     BIGINT NOT NULL REFERENCES tenants (tenant_id) ON DELETE CASCADE,
    name          VARCHAR(100) NOT NULL,
    kind          VARCHAR(50) NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'pending',
    endpoint      TEXT NOT NULL DEFAULT '',
    version       VARCHAR(50) NOT NULL DEFAULT '',
    metadata      JSONB NOT NULL DEFAULT '{}',
    registered_at TIMESTAMPTZ,
    created_by    BIGINT,
    updated_by    BIGINT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_services_tenant_name ON services (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_services_tenant_id ON services (tenant_id);
CREATE INDEX IF NOT EXISTS idx_services_status ON services (status);
CREATE INDEX IF NOT EXISTS idx_services_kind ON services (kind);
CREATE INDEX IF NOT EXISTS idx_services_metadata ON services USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_services_created_at ON services (created_at);
CREATE INDEX IF NOT EXISTS idx_services_deleted_at ON services (deleted_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS services;
