/**
 * The hosted-identity OAuth2 client.
 *
 * The console is a PUBLIC client: authorization code + PKCE (S256), no client
 * secret, and deliberately NO refresh token (`offline_access` is never
 * requested). An administrative surface must not hold a long-lived credential,
 * so continuity across a reload comes from the identity SSO session via a
 * `prompt=none` re-authorization rather than from a stored refresh token.
 *
 * The resulting access token is used as a bearer against core's own `/api/v1`,
 * which verifies it against Auth's JWKS + issuer + audience and then maps the
 * principal's grants onto its `core:*` route permissions.
 *
 * NOTHING IN HERE LOGS. Not the code, not the verifier, not the token, not the
 * failure — a `console.error(err)` on a token-endpoint rejection puts an
 * authorization code in the browser console and in any error-reporting SDK
 * hooked to it. Failures are returned as booleans and thrown as bare errors.
 */

import axios from 'axios'
import type { IdentityConfig } from '@/services/api/config'
import { setAccessToken, setIdTokenHint } from './tokenStore'
import {
  claimSignInAttempt,
  clearSignInAttempts,
  consoleRedirectUri,
  consumePendingOAuthFlow,
  discardPendingOAuthFlow,
  pkceChallenge,
  randomOAuthValue,
  savePendingOAuthFlow,
} from './oauthFlow'

interface OAuthTokenResponse {
  access_token: string
  id_token?: string
  token_type?: string
  expires_in?: number
  scope?: string
}

const BASE_SCOPE = 'openid profile email'

function scopeFor(identity: IdentityConfig): string {
  return identity.extraScope ? `${BASE_SCOPE} ${identity.extraScope}` : BASE_SCOPE
}

async function buildAuthorizeUrl(
  identity: IdentityConfig,
  returnTo: string,
  prompt?: 'none',
): Promise<string> {
  const state = randomOAuthValue()
  const codeVerifier = randomOAuthValue(48)
  const redirectUri = consoleRedirectUri()
  const codeChallenge = await pkceChallenge(codeVerifier)

  savePendingOAuthFlow({
    state,
    codeVerifier,
    clientId: identity.clientId,
    returnTo,
    redirectUri,
  })

  const query = new URLSearchParams({
    response_type: 'code',
    client_id: identity.clientId,
    redirect_uri: redirectUri,
    scope: scopeFor(identity),
    state,
    code_challenge: codeChallenge,
    code_challenge_method: 'S256',
  })
  // The audience names the resource API the token is FOR. Without it Auth may
  // mint a token for its own audience, which core's verifier would reject — an
  // authenticated user who cannot call anything. That failure presents as a 401
  // on every request, which is exactly the shape of a redirect loop, so the
  // attempt budget in `oauthFlow.ts` is what stops it.
  if (identity.audience) query.set('audience', identity.audience)
  if (prompt) query.set('prompt', prompt)

  return `${identity.issuerUrl}/authorize?${query.toString()}`
}

/**
 * Sends the browser to the identity app to sign in.
 *
 * Returns false WITHOUT navigating when the attempt budget is spent. Callers
 * must treat that as terminal and render a message: continuing to navigate is
 * the loop the budget exists to break.
 */
export async function startLogin(identity: IdentityConfig, returnTo: string): Promise<boolean> {
  if (!claimSignInAttempt()) return false
  const url = await buildAuthorizeUrl(identity, returnTo)
  window.location.assign(url)
  return true
}

type SilentOAuthMessage = {
  type: 'maintainerd:oauth:silent'
  state: string
  redirect_uri?: string
  error?: string
}

/**
 * Non-interactive authorization in a hidden identity iframe.
 *
 * Succeeds when the identity SSO session is still live and consent was already
 * given; any login/consent requirement resolves `false` so the caller can fall
 * back to a visible redirect. This is what makes "no persisted token" cost the
 * operator nothing on a page reload.
 *
 * It spends an attempt from the same budget as a visible sign-in, because a
 * silent authorization that succeeds and still yields a 401 is the fastest
 * possible loop — no user interaction slows it down.
 */
export async function trySilentLogin(identity: IdentityConfig, returnTo: string): Promise<boolean> {
  if (!claimSignInAttempt()) return false
  const authorizeUrl = await buildAuthorizeUrl(identity, returnTo, 'none')
  const expectedOrigin = new URL(identity.issuerUrl).origin
  const iframe = document.createElement('iframe')
  iframe.hidden = true
  iframe.setAttribute('aria-hidden', 'true')
  iframe.title = 'silent authorization'

  return new Promise<boolean>((resolve) => {
    let settled = false
    const finish = (value: boolean) => {
      if (settled) return
      settled = true
      window.clearTimeout(timeout)
      window.removeEventListener('message', onMessage)
      iframe.remove()
      resolve(value)
    }
    const onMessage = async (event: MessageEvent<SilentOAuthMessage>) => {
      // Origin AND source are both checked: any page can postMessage to this
      // window, and accepting an authorization result from an unverified sender
      // would be accepting a token address from an attacker.
      if (event.origin !== expectedOrigin || event.source !== iframe.contentWindow) return
      const message = event.data
      if (!message || message.type !== 'maintainerd:oauth:silent' || !message.state) return
      if (message.error || !message.redirect_uri) {
        discardPendingOAuthFlow(message.state)
        finish(false)
        return
      }
      const redirect = new URL(message.redirect_uri)
      const code = redirect.searchParams.get('code')
      const state = redirect.searchParams.get('state')
      if (!code || !state || state !== message.state) {
        discardPendingOAuthFlow(message.state)
        finish(false)
        return
      }
      const flow = consumePendingOAuthFlow(state)
      if (!flow) {
        finish(false)
        return
      }
      try {
        await exchangeAuthorizationCode(identity, {
          code,
          redirectUri: flow.redirectUri,
          codeVerifier: flow.codeVerifier,
        })
        finish(true)
      } catch {
        finish(false)
      }
    }
    const timeout = window.setTimeout(() => finish(false), 5000)
    window.addEventListener('message', onMessage)
    document.body.appendChild(iframe)
    iframe.src = authorizeUrl
  })
}

/**
 * Exchanges an authorization code for an access token and puts it in the
 * in-memory store. The response body is never logged and never persisted.
 */
export async function exchangeAuthorizationCode(
  identity: IdentityConfig,
  params: { code: string; redirectUri: string; codeVerifier: string },
): Promise<void> {
  const form = new URLSearchParams({
    grant_type: 'authorization_code',
    code: params.code,
    redirect_uri: params.redirectUri,
    code_verifier: params.codeVerifier,
    client_id: identity.clientId,
  })

  // A bare axios call, NOT the app's api client: this goes to Auth rather than
  // core, must not carry core's bearer interceptor, and must not run the 401
  // handler — a rejected exchange is a failed sign-in, not an expired session,
  // and routing it through the re-authentication path would loop.
  const response = await axios.post<OAuthTokenResponse>(identity.tokenUrl, form, {
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      // The response carries a bearer token; no-store keeps it out of any disk
      // or proxy cache that would otherwise apply a default heuristic.
      'Cache-Control': 'no-store',
    },
  })

  setAccessToken(response.data.access_token, response.data.expires_in)
  setIdTokenHint(response.data.id_token)
  // The sequence completed. A 401 an hour from now is an expiry, not a loop, and
  // deserves a fresh budget.
  clearSignInAttempts()
}

/**
 * RP-initiated logout. The id_token hint is best effort — logout still works
 * without it, the hint just lets identity end the right session precisely.
 */
export function endSessionUrl(identity: IdentityConfig, idTokenHint: string | null): string {
  const query = new URLSearchParams({
    client_id: identity.clientId,
    post_logout_redirect_uri: window.location.origin,
  })
  if (idTokenHint) query.set('id_token_hint', idTokenHint)
  return `${identity.issuerUrl}/end-session?${query.toString()}`
}
