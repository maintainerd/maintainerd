-- name: GetControlPlane :one
SELECT * FROM control_plane WHERE id = 1;

-- name: UpsertControlPlane :one
--
-- AN EMPTY control_private_key_pem MEANS "LEAVE IT ALONE", never "erase it".
--
-- The control key is core's private_key_jwt signing key, and auth holds the
-- matching JWKS. Overwriting it with '' would leave auth verifying against a
-- public key whose private half no longer exists — every control-plane call
-- refused, permanently, with no way to re-mint without re-registering in auth.
-- Orchestrator.controlKey already refuses to regenerate over a stored key for
-- that reason; this is the same rule enforced at the write instead of trusting
-- every caller to pass the key back. A caller that only means to record setup
-- completion has no business carrying a private key around to do it.
--
-- setup_completed_at is NOT given the same treatment: it is written once with a
-- real timestamp and there is no path that legitimately clears it.
INSERT INTO control_plane (id, auth_tenant_uuid, data, control_private_key_pem, deployment_mode, setup_completed_at)
VALUES (1, $1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
SET auth_tenant_uuid        = EXCLUDED.auth_tenant_uuid,
    data                    = EXCLUDED.data,
    control_private_key_pem = COALESCE(
                                  NULLIF(EXCLUDED.control_private_key_pem, ''),
                                  control_plane.control_private_key_pem
                              ),
    deployment_mode         = EXCLUDED.deployment_mode,
    setup_completed_at      = EXCLUDED.setup_completed_at,
    updated_at              = now()
RETURNING *;
