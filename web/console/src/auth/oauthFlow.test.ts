import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  claimSignInAttempt,
  clearSignInAttempts,
  consumePendingOAuthFlow,
  discardPendingOAuthFlow,
  pkceChallenge,
  randomOAuthValue,
  safeReturnTo,
  savePendingOAuthFlow,
  signInAttemptsExhausted,
  type PendingOAuthFlow,
} from './oauthFlow'

// jsdom's window.crypto has getRandomValues but no SubtleCrypto, and PKCE S256
// is a real SHA-256 — stubbing the digest would test the stub. Node's WebCrypto
// is the same API the browser provides.
if (!window.crypto?.subtle) {
  Object.defineProperty(window, 'crypto', { value: webcrypto, configurable: true })
}

const flow = (overrides: Partial<PendingOAuthFlow> = {}): PendingOAuthFlow => ({
  state: 'state-abc',
  codeVerifier: 'verifier-xyz',
  clientId: 'core-console',
  returnTo: '/dashboard',
  redirectUri: 'https://console.maintainerd.local/auth/callback',
  ...overrides,
})

beforeEach(() => {
  window.sessionStorage.clear()
  vi.useRealTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

/**
 * `safeReturnTo` is the open-redirect guard on the post-login hop. These cases
 * are the ones that actually get exploited, so they are pinned.
 */
describe('safeReturnTo', () => {
  it('accepts an absolute local path', () => {
    expect(safeReturnTo('/dashboard')).toBe('/dashboard')
    expect(safeReturnTo('/tenants?page=2')).toBe('/tenants?page=2')
  })

  it('rejects a protocol-relative URL', () => {
    expect(safeReturnTo('//evil.example')).toBeNull()
  })

  it('rejects a backslash-prefixed host', () => {
    expect(safeReturnTo('/\\evil.example')).toBeNull()
  })

  it('rejects an absolute URL', () => {
    expect(safeReturnTo('https://evil.example/dashboard')).toBeNull()
  })

  it('rejects empty and relative values', () => {
    expect(safeReturnTo('')).toBeNull()
    expect(safeReturnTo(null)).toBeNull()
    expect(safeReturnTo(undefined)).toBeNull()
    expect(safeReturnTo('dashboard')).toBeNull()
  })
})

describe('pkceChallenge', () => {
  // RFC 7636 appendix B. If this vector ever fails, the authorize request is
  // sending a challenge maintainerd-auth cannot match against the verifier.
  it('matches the RFC 7636 S256 test vector', async () => {
    await expect(pkceChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).resolves.toBe(
      'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM',
    )
  })

  it('produces base64url with no padding or unsafe characters', async () => {
    const challenge = await pkceChallenge(randomOAuthValue(48))
    expect(challenge).toMatch(/^[A-Za-z0-9_-]+$/)
    expect(challenge).not.toContain('=')
  })
})

describe('randomOAuthValue', () => {
  it('does not repeat', () => {
    const values = new Set(Array.from({ length: 25 }, () => randomOAuthValue()))
    expect(values.size).toBe(25)
  })
})

describe('the pending flow', () => {
  it('returns the flow when the state matches', () => {
    savePendingOAuthFlow(flow())
    expect(consumePendingOAuthFlow('state-abc')?.codeVerifier).toBe('verifier-xyz')
  })

  // The state comparison IS the CSRF check. It must fail closed rather than fall
  // back to whatever flow happens to be stored.
  it('rejects a mismatched state and leaves the flow untouched', () => {
    savePendingOAuthFlow(flow())
    expect(consumePendingOAuthFlow('state-from-an-attacker')).toBeNull()
    // The legitimate flow is still there — a forged callback must not be able to
    // destroy a real in-flight sign-in either.
    expect(consumePendingOAuthFlow('state-abc')).not.toBeNull()
  })

  // The verifier is single-use: a replayed callback (a reload, a second tab, an
  // attacker resubmitting an intercepted code) must find nothing to exchange.
  it('consumes the verifier exactly once', () => {
    savePendingOAuthFlow(flow())
    expect(consumePendingOAuthFlow('state-abc')).not.toBeNull()
    expect(consumePendingOAuthFlow('state-abc')).toBeNull()
    expect(window.sessionStorage.getItem('maintainerd.core.console.oauth.pending')).toBeNull()
  })

  it('returns null when nothing is pending', () => {
    expect(consumePendingOAuthFlow('state-abc')).toBeNull()
  })

  it('survives a corrupted entry instead of throwing', () => {
    window.sessionStorage.setItem('maintainerd.core.console.oauth.pending', 'not json')
    expect(consumePendingOAuthFlow('state-abc')).toBeNull()
    expect(() => discardPendingOAuthFlow('state-abc')).not.toThrow()
  })

  it('discards only the matching state', () => {
    savePendingOAuthFlow(flow())
    discardPendingOAuthFlow('some-other-state')
    expect(consumePendingOAuthFlow('state-abc')).not.toBeNull()
  })

  // Nothing about the pending flow may outlive the tab. sessionStorage is the
  // ceiling; localStorage would leave a verifier on disk.
  it('never writes to localStorage', () => {
    savePendingOAuthFlow(flow())
    expect(window.localStorage.length).toBe(0)
  })
})

/**
 * The loop breaker.
 *
 * A sign-in is a full-page navigation, so every in-memory latch resets on the way
 * back. This budget is the only thing that can observe "we have been here before"
 * and is what turns an unattended bounce into a screen someone can read.
 */
describe('the sign-in attempt budget', () => {
  it('allows three attempts and refuses the fourth', () => {
    expect(claimSignInAttempt()).toBe(true)
    expect(claimSignInAttempt()).toBe(true)
    expect(claimSignInAttempt()).toBe(true)
    expect(claimSignInAttempt()).toBe(false)
    expect(claimSignInAttempt()).toBe(false)
  })

  it('reports exhaustion only once the budget is spent', () => {
    expect(signInAttemptsExhausted()).toBe(false)
    claimSignInAttempt()
    claimSignInAttempt()
    expect(signInAttemptsExhausted()).toBe(false)
    claimSignInAttempt()
    expect(signInAttemptsExhausted()).toBe(true)
  })

  // Exhaustion has to survive the navigation, or it cannot see the loop at all:
  // module state dies with the page, sessionStorage does not.
  it('persists across a simulated page load', () => {
    claimSignInAttempt()
    claimSignInAttempt()
    claimSignInAttempt()
    // A reload re-imports the module but keeps sessionStorage — which is the
    // whole point. Reading it back is the same observation a fresh load makes.
    expect(signInAttemptsExhausted()).toBe(true)
  })

  it('starts a fresh window once the old one has expired', () => {
    vi.useFakeTimers()
    claimSignInAttempt()
    claimSignInAttempt()
    claimSignInAttempt()
    expect(signInAttemptsExhausted()).toBe(true)
    vi.advanceTimersByTime(61_000)
    // A sign-in a minute later is a new sequence, not the same loop.
    expect(signInAttemptsExhausted()).toBe(false)
    expect(claimSignInAttempt()).toBe(true)
  })

  it('resets on a completed sign-in', () => {
    claimSignInAttempt()
    claimSignInAttempt()
    claimSignInAttempt()
    clearSignInAttempts()
    expect(signInAttemptsExhausted()).toBe(false)
    expect(claimSignInAttempt()).toBe(true)
  })

  // A storage failure must fail CLOSED. With no ledger there is no budget, and
  // proceeding without a budget is exactly the unbounded loop.
  it('refuses the attempt when the ledger cannot be written', () => {
    // Spied on the prototype rather than the instance: jsdom's Storage is proxied,
    // so an own-property stub on `window.sessionStorage` is not what the call site
    // resolves to.
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage is full')
    })
    expect(claimSignInAttempt()).toBe(false)
    setItem.mockRestore()
  })
})
