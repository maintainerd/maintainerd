-- name: GetControlPlane :one
SELECT * FROM control_plane WHERE id = 1;

-- name: UpsertControlPlane :one
INSERT INTO control_plane (id, auth_tenant_uuid, data, control_private_key_pem, deployment_mode, setup_completed_at)
VALUES (1, $1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
SET auth_tenant_uuid        = EXCLUDED.auth_tenant_uuid,
    data                    = EXCLUDED.data,
    control_private_key_pem = EXCLUDED.control_private_key_pem,
    deployment_mode         = EXCLUDED.deployment_mode,
    setup_completed_at      = EXCLUDED.setup_completed_at,
    updated_at              = now()
RETURNING *;
