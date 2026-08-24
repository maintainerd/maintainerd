import { ApiError, get, post } from '../client'
import { SETUP_TOKEN_HEADER } from '../config'
import { unwrap } from '../_lib/unwrap'
import type { ApiResponse } from '../types'
import type { SetupStatus, SetupResult, RunSetupRequest } from './types'

/**
 * The first-run wizard's two calls.
 *
 * This is the one surface core's bearer guard does not cover, and it cannot be:
 * it provisions the Auth that would mint the bearer. It carries its own
 * shared-secret gate instead — core's `CORE_SETUP_TOKEN`, presented in
 * `X-Setup-Token` — which is the same bootstrap-token pattern maintainerd-auth
 * and maintainerd-secret use for their own wizards.
 *
 * THE TOKEN IS A CREDENTIAL AND IS TREATED AS ONE. It is a parameter and never
 * module state: nothing here stores it, logs it or puts it in a URL, so its
 * lifetime is the wizard's own. An anonymous status read returns exactly one bit
 * (`completed`) because everything else — the control-plane and client IDs, the
 * signing JWKS, the deployment substrate — maps the install for an attacker.
 */

const base = '/setup'

/**
 * Reads the setup status.
 *
 * The token is OPTIONAL and the header is omitted entirely when it is absent: the
 * boot-time gate only needs the anonymous `completed` bit, and an empty
 * `X-Setup-Token` is not a credential the gate can compare. Supplying it widens
 * the answer to the full status.
 */
export function getSetupStatus(setupToken?: string): Promise<SetupStatus> {
  return get<ApiResponse<SetupStatus>>(`${base}/status`, {
    headers: setupToken ? { [SETUP_TOKEN_HEADER]: setupToken } : undefined,
    skipSessionRecovery: true,
  }).then((r) => unwrap(r, 'fetch setup status'))
}

/**
 * Runs the full provisioning.
 *
 * THE TOKEN IS OPTIONAL, and the header is omitted entirely when absent — the
 * same rule as the status read, for a stronger reason. Core is the STARTING POINT
 * of an install: at first boot there is no auth, no tenant and no admin, so there
 * is nowhere a credential could have come from. A fresh install therefore has no
 * token to send, and core's gate is built for exactly that — an empty
 * CORE_SETUP_TOKEN leaves the surface open in development and DISABLES the
 * trigger outside it, so the deployed case is fail-closed rather than anonymous.
 *
 * Sending an empty header instead of omitting it would be worse than useless: the
 * gate compares in constant time and treats "" as no credential, so it would read
 * as a refused token rather than as an unauthenticated first run.
 */
export function runSetup(data: RunSetupRequest, setupToken?: string): Promise<SetupResult> {
  return post<ApiResponse<SetupResult>>(base, data, {
    headers: setupToken ? { [SETUP_TOKEN_HEADER]: setupToken } : undefined,
    skipSessionRecovery: true,
  }).then((r) => unwrap(r, 'run setup'))
}

/**
 * What the wizard shows when the gate refuses the bootstrap token.
 *
 * The server's own text — "missing or invalid setup token" — names the problem
 * without naming the fix, and the fix is a specific variable on a specific
 * service. A 401 is unambiguous here BECAUSE this surface has no bearer of its own
 * to expire: the setup token is the only credential it accepts, so a 401 can only
 * be about that token.
 */
export const SETUP_TOKEN_REJECTED_MESSAGE =
  'That setup token was refused. Enter the value core was started with as CORE_SETUP_TOKEN.'

/** True when a setup failure is the gate rejecting the presented token. */
export function isSetupTokenRejected(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401
}
