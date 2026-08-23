/**
 * OAuth2 authorization-code + PKCE plumbing.
 *
 * What is persisted here is the PENDING flow only — the `state` and the PKCE
 * `code_verifier`, in sessionStorage, for the seconds between leaving for the
 * identity app and coming back. Neither is a credential once the exchange has
 * happened, and the entry is consumed (removed) on the way back. The access
 * token itself is never persisted; see `auth/tokenStore.ts`.
 *
 * The sign-in attempt ledger at the bottom is persisted for the same reason and
 * is the only thing in this app that can actually see a redirect loop.
 */

export const OAUTH_CALLBACK_ROUTE = '/auth/callback'

/** Where a signed-in operator lands when no specific page was requested. */
export const POST_LOGIN_ROUTE = '/dashboard'

/**
 * Constrain a stored return-to to a same-origin path before it drives a
 * post-login redirect. Only an absolute local path (`/foo`) is allowed —
 * protocol-relative (`//host`), backslash tricks (`/\host`), and absolute URLs
 * are rejected so a crafted `returnTo` cannot become an open redirect. Returns
 * `null` when unsafe; callers fall back to the default post-auth route.
 */
export function safeReturnTo(value: string | null | undefined): string | null {
  if (!value || !value.startsWith('/')) return null
  if (value.startsWith('//') || value.startsWith('/\\')) return null
  return value
}

export interface PendingOAuthFlow {
  state: string
  codeVerifier: string
  clientId: string
  returnTo: string
  redirectUri: string
}

const PENDING_KEY = 'maintainerd.core.console.oauth.pending'

function base64Url(bytes: Uint8Array): string {
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return window.btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function randomOAuthValue(byteLength = 32): string {
  const bytes = new Uint8Array(byteLength)
  window.crypto.getRandomValues(bytes)
  return base64Url(bytes)
}

export async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await window.crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64Url(new Uint8Array(digest))
}

export function consoleRedirectUri(): string {
  return `${window.location.origin}${OAUTH_CALLBACK_ROUTE}`
}

export function savePendingOAuthFlow(flow: PendingOAuthFlow): void {
  window.sessionStorage.setItem(PENDING_KEY, JSON.stringify(flow))
}

/**
 * Reads back the pending flow for `state` and removes it. A mismatched state is
 * treated as no flow at all — that comparison is the CSRF check, so it fails
 * closed rather than falling back to whatever happens to be stored. The removal
 * is what makes the verifier single-use: a replayed callback finds nothing.
 */
export function consumePendingOAuthFlow(state: string): PendingOAuthFlow | null {
  try {
    const raw = window.sessionStorage.getItem(PENDING_KEY)
    if (!raw) return null
    const flow = JSON.parse(raw) as PendingOAuthFlow
    if (flow.state !== state) return null
    window.sessionStorage.removeItem(PENDING_KEY)
    return flow
  } catch {
    return null
  }
}

export function discardPendingOAuthFlow(state: string): void {
  try {
    const raw = window.sessionStorage.getItem(PENDING_KEY)
    if (!raw) return
    const flow = JSON.parse(raw) as PendingOAuthFlow
    if (flow.state === state) window.sessionStorage.removeItem(PENDING_KEY)
  } catch {
    window.sessionStorage.removeItem(PENDING_KEY)
  }
}

/* -------------------------------------------------------------------------- */
/* Redirect-loop budget                                                        */
/* -------------------------------------------------------------------------- */

const ATTEMPT_KEY = 'maintainerd.core.console.oauth.attempts'

/**
 * Three tries inside a minute. Two is a legitimate sequence (a silent
 * authorization declines, then a visible one runs); a third inside the same
 * minute is already unusual, and a fourth is a machine, not a person.
 */
const ATTEMPT_LIMIT = 3
const ATTEMPT_WINDOW_MS = 60_000

interface AttemptLedger {
  /** Epoch ms of the first attempt in the current window. */
  first: number
  count: number
}

/**
 * THE LEDGER IS PERSISTED BECAUSE AN IN-MEMORY COUNTER CANNOT SEE A LOOP.
 *
 * A sign-in is a full-page navigation to the identity app and back, which tears
 * down the JavaScript context and resets every module-level variable — including
 * the "only re-authenticate once" latch in `services/api/client.ts`. So the loop
 * that actually bites is the cross-navigation one: land, call the API, get a 401,
 * redirect to identity, come back with a fresh token, call the API, get a 401
 * again. Each hop looks like a first attempt to everything except sessionStorage.
 *
 * The 401 in that loop is real and is not fixable from the browser — a token
 * minted for the wrong audience, or a principal with no `core:*` permission
 * mapping, produces it forever. The console's job is to stop and say so.
 *
 * sessionStorage rather than localStorage: the budget belongs to this tab's
 * sign-in sequence, and a second tab deserves its own attempts.
 */
function readLedger(): AttemptLedger | null {
  try {
    const raw = window.sessionStorage.getItem(ATTEMPT_KEY)
    if (!raw) return null
    const ledger = JSON.parse(raw) as AttemptLedger
    if (typeof ledger.first !== 'number' || typeof ledger.count !== 'number') return null
    // An expired window is not a loop; it is a new sequence.
    if (Date.now() - ledger.first > ATTEMPT_WINDOW_MS) return null
    return ledger
  } catch {
    return null
  }
}

/** True when the budget is spent, so the caller must NOT redirect again. */
export function signInAttemptsExhausted(): boolean {
  const ledger = readLedger()
  return ledger !== null && ledger.count >= ATTEMPT_LIMIT
}

/**
 * Records one sign-in redirect and reports whether it may proceed. Returns false
 * once the budget is spent — the caller then renders a terminal state instead of
 * navigating, which is what converts an infinite bounce into a legible message.
 */
export function claimSignInAttempt(): boolean {
  const ledger = readLedger()
  const next: AttemptLedger = ledger
    ? { first: ledger.first, count: ledger.count + 1 }
    : { first: Date.now(), count: 1 }
  try {
    window.sessionStorage.setItem(ATTEMPT_KEY, JSON.stringify(next))
  } catch {
    // A storage failure must not become a redirect loop, so refuse the attempt
    // rather than proceeding with no budget at all.
    return false
  }
  return next.count <= ATTEMPT_LIMIT
}

/**
 * Clears the budget. Called once an access token is actually in hand: the
 * sequence completed, so the next 401 (an expiry an hour later) starts fresh
 * rather than inheriting a spent budget.
 */
export function clearSignInAttempts(): void {
  try {
    window.sessionStorage.removeItem(ATTEMPT_KEY)
  } catch {
    // Nothing to recover — a stale ledger only costs one unnecessary attempt.
  }
}
