-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_templates (
    template_id   BIGSERIAL PRIMARY KEY,
    template_uuid UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    name          VARCHAR(100) NOT NULL,
    version       VARCHAR(50) NOT NULL DEFAULT 'v1',
    capability    VARCHAR(63) NOT NULL DEFAULT '',
    image         TEXT NOT NULL,
    parameters    JSONB NOT NULL DEFAULT '[]',
    spec          JSONB NOT NULL DEFAULT '{}',
    metadata      JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_deployment_templates_name_version ON deployment_templates (name, version) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_deployment_templates_capability ON deployment_templates (capability) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_deployment_templates_metadata ON deployment_templates USING GIN (metadata);

-- +goose Down
DROP TABLE IF EXISTS deployment_templates;
