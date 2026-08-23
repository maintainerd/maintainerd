import { createContext, useContext } from 'react'
import type { IdentityConfig } from '@/services/api/config'

/**
 * How this console is authenticating against core.
 *
 * WHAT THIS UNION DELIBERATELY DOES NOT CONTAIN is a "the guard is open" state.
 * maintainerd-secret has one, because secret publishes `GET /capabilities` and
 * its console can ask the service whether its guard is enforced. Core exposes no
 * such endpoint (its `authz.Mode` — enforced / dev-open / unavailable — is
 * internal and never serialized), and inferring the server's posture from the
 * absence of settings in this console's OWN config is precisely the bug secret's
 * capability probe was built to remove: it is wrong in both directions, and the
 * dangerous direction is telling an operator "no auth needed" about a service
 * that is refusing every call.
 *
 * So this console does not guess. It runs the flow when it can, and lets core's
 * own answers settle the question:
 *
 *  - `authenticated`    A PKCE access token is held in memory and sent as a
 *                       bearer. Requests may still be refused for lack of a
 *                       grant — that is a 403 and is not this module's business.
 *  - `anonymous`        Identity is configured but no token is held yet. The next
 *                       step is a sign-in redirect.
 *  - `identity-missing` No identity configuration, so this console CANNOT obtain
 *                       a token, and core's posture is unknown. Requests go out
 *                       with no bearer — which is exactly right if core's guard
 *                       is dev-open, and produces a 401 if it is not. Rendering
 *                       proceeds; the shell says auth is unconfigured without
 *                       claiming it is unnecessary.
 *  - `blocked`          Core refused, and this console cannot fix it by signing
 *                       in — either there is no identity configuration to sign in
 *                       with, or sign-in kept being spent without producing a
 *                       usable token. TERMINAL AND STABLE BY DESIGN: it renders a
 *                       message naming what to set, and never redirects.
 */
export type AuthMode = 'authenticated' | 'anonymous' | 'identity-missing' | 'blocked'

/** Why the console is `blocked`. Drives which remedy the screen names. */
export type BlockedReason =
  /** Core enforces tokens (it returned 401) and no identity is configured. */
  | 'identity-missing'
  /** Sign-in ran its budget without yielding a token core accepts. */
  | 'sign-in-failed'

export interface AuthContextValue {
  mode: AuthMode
  /** True once the boot-time silent authorization has settled. */
  ready: boolean
  /** Present only when identity is configured. */
  identity: IdentityConfig | null
  /** Set only in `blocked`; null otherwise. */
  blockedReason: BlockedReason | null
  /**
   * Sends the browser to the identity app. A no-op when there is no identity
   * configuration, and a no-op once the attempt budget is spent — both are
   * states where navigating again would loop.
   */
  signIn: (returnTo?: string) => void
  /** Drops the in-memory token and ends the identity session. */
  signOut: () => void
}

export const AuthContext = createContext<AuthContextValue | null>(null)

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used inside <AuthProvider>')
  return value
}
