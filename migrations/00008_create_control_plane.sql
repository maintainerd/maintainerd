-- +goose Up
-- Singleton row recording what Core learns when it orchestrates setup against
-- Auth: the Auth system tenant, and Core's own control-plane credentials
-- (service principal, private_key_jwt M2M client, resource API, admin role,
-- console client). `data` holds the returned IDs as a JSON object so the set can
-- grow without a migration; `control_private_key_pem` is Core's private_key_jwt
-- signing key (sensitive — belongs in SECRET_PROVIDER long-term).
CREATE TABLE IF NOT EXISTS control_plane (
    id                      INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    auth_tenant_uuid        UUID,
    data                    JSONB NOT NULL DEFAULT '{}',
    control_private_key_pem TEXT  NOT NULL DEFAULT '',
    -- deployment_mode is stamped once at setup from DEPLOYMENT_MODE and is
    -- IMMUTABLE for the life of the install: every reconciled resource was
    -- materialized on that substrate (docker containers vs kubernetes objects),
    -- so flipping the mode later would orphan every running workload while the
    -- agents rebuild the world on a different runtime. Boot refuses to start if
    -- the environment disagrees with this stamp.
    deployment_mode         VARCHAR(20) NOT NULL DEFAULT 'docker',
    setup_completed_at      TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS control_plane;
