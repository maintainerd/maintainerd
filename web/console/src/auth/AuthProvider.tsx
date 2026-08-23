import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { IDENTITY_CONFIG } from '@/services/api/config'
import { setUnauthenticatedHandler } from '@/services/api/client'
import AppLoadingScreen from '@/components/layout/AppLoadingScreen'
import { AuthContext, type AuthContextValue, type AuthMode, type BlockedReason } from './authContext'
import { clearAccessToken, getIdTokenHint, hasAccessToken, subscribeToToken } from './tokenStore'
import { endSessionUrl, startLogin, trySilentLogin } from './oauthClient'
import {
  OAUTH_CALLBACK_ROUTE,
  POST_LOGIN_ROUTE,
  clearSignInAttempts,
  safeReturnTo,
  signInAttemptsExhausted,
} from './oauthFlow'

/**
 * The session gate.
 *
 * On boot it decides ONE thing and blocks rendering until it has: does this
 * console hold a credential, and if not, can it get one silently? Rendering the
 * app before that verdict is what produces the classic flicker — a protected
 * page paints, a 401 lands, and the app bounces mid-paint.
 *
 * NO CAPABILITY PROBE EXISTS ON CORE, AND NOTHING HERE INVENTS ONE.
 * maintainerd-secret's provider asks `GET /capabilities` whether the service's
 * guard is enforced, because it had shipped the alternative — concluding "the
 * guard must be dev-open" from the absence of identity settings in the console's
 * own configuration — and that inference was wrong in both directions. Core has
 * the same three-state guard internally (`internal/platform/authz`: enforced /
 * dev-open / unavailable) but publishes it nowhere, so this provider makes no
 * claim about the server at all. Instead:
 *
 *   identity configured      → run the flow. A dev-open core ignores the bearer
 *                              and serves the request anyway, so this is correct
 *                              under either posture.
 *   identity NOT configured  → render, send no bearer, and wait. A dev-open core
 *                              answers normally; an enforced core answers 401,
 *                              and THAT is what moves the console to `blocked` —
 *                              an observation, not a guess.
 *
 * The cost of the missing endpoint is one wasted request before the truth is
 * known, and a boot that cannot skip a needless silent authorization. Both are
 * strictly better than a confident wrong answer. See the report note asking for
 * `GET /api/v1/capabilities` on core.
 *
 * Recovery from an expired token is a `prompt=none` re-authorization, not a
 * refresh-token call: the console deliberately holds no refresh token (see
 * `tokenStore.ts`). While the identity SSO session is alive the operator sees
 * nothing; once it is gone they land on a visible sign-in.
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const identity = IDENTITY_CONFIG
  const [loginSettled, setLoginSettled] = useState(false)
  const [tokenTick, setTokenTick] = useState(0)
  const [blockedReason, setBlockedReason] = useState<BlockedReason | null>(null)
  const startedRef = useRef(false)

  const signIn = useCallback(
    (returnTo?: string) => {
      // No identity means no authorize endpoint to go to. Navigating anyway is
      // the "redirect loop to nowhere" this branch exists to prevent.
      if (!identity) {
        setBlockedReason('identity-missing')
        return
      }
      const target =
        safeReturnTo(returnTo) ??
        safeReturnTo(`${window.location.pathname}${window.location.search}`) ??
        POST_LOGIN_ROUTE
      void startLogin(identity, target).then((started) => {
        // startLogin refused: the attempt budget is spent, so the 401 that
        // triggered this is not something another sign-in can fix.
        if (!started) setBlockedReason('sign-in-failed')
      })
    },
    [identity],
  )

  const signOut = useCallback(() => {
    const hint = getIdTokenHint()
    clearAccessToken()
    // An explicit sign-out is a deliberate fresh start; the next sign-in should
    // not inherit a spent budget from whatever went wrong before it.
    clearSignInAttempts()
    if (!identity) return
    window.location.assign(endSessionUrl(identity, hint))
  }, [identity])

  // Boot: try to pick the session back up without showing anything.
  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true
    if (!identity) {
      setLoginSettled(true)
      return
    }
    // The callback route is mid-exchange — attempting a second authorization
    // here would race the one already in flight and consume its pending state.
    if (window.location.pathname === OAUTH_CALLBACK_ROUTE) {
      setLoginSettled(true)
      return
    }
    if (hasAccessToken()) {
      setLoginSettled(true)
      return
    }
    // Arriving with the budget already spent means the previous page load ended
    // in a sign-in that did not stick. Spending the last hop on another silent
    // authorization would hide the failure behind one more redirect.
    if (signInAttemptsExhausted()) {
      setBlockedReason('sign-in-failed')
      setLoginSettled(true)
      return
    }
    trySilentLogin(identity, `${window.location.pathname}${window.location.search}`)
      .catch(() => false)
      .finally(() => setLoginSettled(true))
  }, [identity])

  // Re-render whenever the in-memory token is set or cleared.
  useEffect(() => subscribeToToken(() => setTokenTick((tick) => tick + 1)), [])

  // A 401 from any API call lands here. The token is already cleared by the
  // interceptor; this decides where the operator goes next. The interceptor
  // calls this at most once per page load, and `signIn` is itself budgeted, so
  // the two together bound both the within-page fan-out and the across-page loop.
  useEffect(() => {
    setUnauthenticatedHandler(() => {
      if (!identity) {
        // Core just proved its guard is ENFORCED — it refused an unauthenticated
        // request — and this console has no way to obtain a token. This is the
        // state secret's capability probe reports up front and core cannot, so
        // it is learned here instead. Terminal: there is nowhere to redirect to.
        setBlockedReason('identity-missing')
        return
      }
      signIn(`${window.location.pathname}${window.location.search}`)
    })
    return () => setUnauthenticatedHandler(null)
  }, [identity, signIn])

  const value = useMemo<AuthContextValue>(() => {
    // tokenTick is read so the memo re-runs when the token store changes.
    void tokenTick
    let mode: AuthMode = 'anonymous'
    if (blockedReason) {
      mode = 'blocked'
    } else if (!identity) {
      mode = 'identity-missing'
    } else if (hasAccessToken()) {
      mode = 'authenticated'
    }
    return {
      mode,
      ready: loginSettled,
      identity,
      blockedReason,
      signIn,
      signOut,
    }
  }, [blockedReason, identity, loginSettled, signIn, signOut, tokenTick])

  if (!loginSettled) return <AppLoadingScreen message="Checking your session" />

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
