/** The data Core recorded from provisioning (subset the console cares about). */
export interface SetupResult {
  auth_tenant_id?: string
  auth_admin_user_id?: string
  control_client_id?: string
  console_client_id?: string
  completed_at?: string
  already_complete?: boolean
}

/**
 * GET /setup/status is slimmed for unauthenticated callers: only `completed`
 * is always present. The full shape (enabled/result/deployment_mode) requires
 * the setup token or an admin token — the control-plane IDs and JWKS are not
 * public data.
 */
export interface SetupStatus {
  /** Whether Core has recorded a completed setup. */
  completed: boolean
  /** Whether on-boot auto-provisioning is enabled (authenticated callers only). */
  enabled?: boolean
  /** Immutable install substrate stamp (authenticated callers only). */
  deployment_mode?: string
  result?: SetupResult
}

/** The tenant + admin the wizard collects. All optional server-side (env fallback). */
export interface RunSetupRequest {
  tenant_name?: string
  tenant_display_name?: string
  admin_username?: string
  admin_fullname?: string
  admin_email?: string
  admin_password?: string
}
