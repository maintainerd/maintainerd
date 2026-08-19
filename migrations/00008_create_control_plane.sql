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
    setup_completed_at      TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS control_plane;
