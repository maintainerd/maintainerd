-- +goose Up
-- A project groups a tenant's resources (tenant -> project -> resource). It is
-- the ownership/organizational boundary the resource inventory keys to.
CREATE TABLE IF NOT EXISTS projects (
    project_id   BIGSERIAL PRIMARY KEY,
    project_uuid UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id    BIGINT NOT NULL REFERENCES tenants (tenant_id) ON DELETE CASCADE,
    name         VARCHAR(63) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    status       VARCHAR(20) NOT NULL DEFAULT 'active',
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_by   BIGINT,
    updated_by   BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_tenant_name ON projects (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_tenant_id ON projects (tenant_id);
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects (status);
CREATE INDEX IF NOT EXISTS idx_projects_metadata ON projects USING GIN (metadata);
CREATE INDEX IF NOT EXISTS idx_projects_created_at ON projects (created_at);
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects (deleted_at) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS projects;
