-- +goose Up
-- System services are platform-critical (the IAM Auth, the system Docker control,
-- ...). Core must keep them running and must refuse to remove them. Regular
-- services (is_system = FALSE) are ordinary app services — e.g. a tenant's
-- Cognito-style Auth — and there can be many of the same kind.
ALTER TABLE services ADD COLUMN IF NOT EXISTS is_system BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_services_is_system ON services (is_system);
-- Exactly one system service of each kind may exist platform-wide: one system
-- Auth (the IAM), one system Docker, etc. Non-system services are unconstrained.
CREATE UNIQUE INDEX IF NOT EXISTS uq_services_single_system_kind ON services (kind) WHERE is_system = TRUE AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS uq_services_single_system_kind;
DROP INDEX IF EXISTS idx_services_is_system;
ALTER TABLE services DROP COLUMN IF EXISTS is_system;
