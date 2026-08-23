import { useEffect, type ReactNode } from 'react'
import { AxiosError, type AxiosAdapter, type AxiosResponse } from 'axios'
import { act, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { IdentityConfig } from '@/services/api/config'
import { apiClient } from '@/services/api/client'
import { AuthProvider } from './AuthProvider'
import { RequireAuth } from './RequireAuth'
import { claimSignInAttempt } from './oauthFlow'
import { clearAccessToken, setAccessToken } from './tokenStore'

const IDENTITY: IdentityConfig = {
  issuerUrl: 'https://identity.test',
  tokenUrl: 'https://identity.test/api/v1/oauth/token',
  clientId: 'core-console',
  audience: 'core-api',
  extraScope: '',
}

// Swapped per test so both postures are exercised: an identity-configured
// console (which may redirect) and one with none (which must never redirect).
const identity: { value: IdentityConfig | null } = { value: IDENTITY }

vi.mock('@/services/api/config', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/services/api/config')>()
  return {
    ...actual,
    get IDENTITY_CONFIG() {
      return identity.value
    },
  }
})

// The real silent login opens an iframe and waits up to 5s for a postMessage
// that no identity provider is here to send. Only its verdict matters to these
// tests; `startLogin` — the thing that actually navigates — stays real.
vi.mock('./oauthClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./oauthClient')>()
  return { ...actual, trySilentLogin: vi.fn(async () => false) }
})

const unauthorized: AxiosAdapter = async (config) => {
  throw new AxiosError('unauthorized', 'ERR_BAD_REQUEST', config, {}, {
    data: { success: false, error: 'missing bearer token' },
    status: 401,
    statusText: 'Unauthorized',
    headers: {},
    config,
  } as AxiosResponse)
}

/** Stands in for the dashboard: mounts, fans out, and gets refused. */
function Probe({ calls = 1 }: { calls?: number }) {
  useEffect(() => {
    for (let i = 0; i < calls; i += 1) {
      void apiClient.get(`/tenants?n=${i}`, { adapter: unauthorized }).catch(() => undefined)
    }
  }, [calls])
  return <div>dashboard</div>
}

let assign: ReturnType<typeof vi.fn>

/**
 * Rendering is awaited inside `act` because the provider settles asynchronously:
 * the boot-time silent authorization resolves in a microtask, and so does the
 * 401 that the probe's requests produce. Letting those land outside act is what
 * makes React warn — and, worse, would let an assertion run against a provider
 * that has not finished deciding.
 *
 * A REAL /login ROUTE IS PART OF THE FIXTURE, not scenery. RequireAuth's
 * unauthenticated branch renders `<Navigate to="/login">`, so without somewhere
 * for that to land the guard stays mounted, navigates, re-renders, and navigates
 * again — the exact loop these tests exist to rule out, and it will exhaust the
 * heap rather than fail an assertion. Its presence is what proves the redirect
 * terminates.
 */
async function renderGated(children: ReactNode, route = '/dashboard') {
  await act(async () => {
    render(
      <MemoryRouter initialEntries={[route]}>
        <AuthProvider>
          <Routes>
            <Route path="/login" element={<div>login page</div>} />
            <Route path="*" element={<RequireAuth>{children}</RequireAuth>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>,
    )
  })
}

const renderApp = (calls = 1) => renderGated(<Probe calls={calls} />)

beforeEach(() => {
  identity.value = IDENTITY
  clearAccessToken()
  window.sessionStorage.clear()
  assign = vi.fn()
  // jsdom refuses to navigate, and a real navigation would end the test run.
  // Replacing location is also how the number of navigations gets counted.
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: {
      origin: 'https://console.maintainerd.test',
      pathname: '/dashboard',
      search: '',
      href: 'https://console.maintainerd.test/dashboard',
      assign,
    },
  })
})

// The token is reset in beforeEach, NOT here: this hook runs before Testing
// Library's cleanup, so clearing the store now notifies a provider that is still
// mounted, producing a state update outside act(). beforeEach is after cleanup
// and just as thorough.
afterEach(() => {
  window.sessionStorage.clear()
})

describe('a 401 while signed in', () => {
  /**
   * THE HEADLINE PROPERTY. An expired token means one trip to the identity app,
   * not one per in-flight request. Five refusals arrive from a dashboard fan-out
   * and exactly one navigation may leave.
   */
  it('produces exactly one navigation to the identity app, not one per request', async () => {
    setAccessToken('expired-but-present')
    await renderApp(5)

    await waitFor(() => expect(assign).toHaveBeenCalledTimes(1))

    const target = new URL(assign.mock.calls[0][0] as string)
    expect(target.origin).toBe('https://identity.test')
    expect(target.pathname).toBe('/authorize')
    expect(target.searchParams.get('response_type')).toBe('code')
    expect(target.searchParams.get('code_challenge_method')).toBe('S256')
    expect(target.searchParams.get('client_id')).toBe('core-console')
    expect(target.searchParams.get('audience')).toBe('core-api')
    // A public client sends a challenge and never a secret.
    expect(target.searchParams.get('code_challenge')).toBeTruthy()
    expect(target.searchParams.has('client_secret')).toBe(false)
    // The page being asked for is what the callback returns to.
    expect(target.searchParams.get('state')).toBeTruthy()

    // And it stays at one — no late arrival adds a second.
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(assign).toHaveBeenCalledTimes(1)
  })

  /**
   * THE LOOP TEST. A page load that arrives with the budget already spent is the
   * signature of a bounce: sign in, get refused, sign in again. The console must
   * stop and explain rather than take the next hop.
   */
  it('stops navigating once the attempt budget is spent and says why', async () => {
    // Three hops already happened on previous page loads.
    claimSignInAttempt()
    claimSignInAttempt()
    claimSignInAttempt()

    setAccessToken('expired-but-present')
    await renderApp(3)

    await waitFor(() => expect(screen.getByText(/sign-in unavailable/i)).toBeInTheDocument())
    expect(screen.getByText(/did not give access/i)).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })
})

describe('a 401 with no identity configuration', () => {
  /**
   * The state core cannot report up front, because it publishes no capability
   * endpoint: the guard IS enforced and this console cannot obtain a token. It is
   * learned from the refusal, and the only correct output is a screen — there is
   * no authorize endpoint to navigate to, so a redirect here would be a bounce to
   * nowhere.
   */
  it('never navigates and names the settings to set', async () => {
    identity.value = null
    await renderApp(2)

    await waitFor(() => expect(screen.getByText(/sign-in unavailable/i)).toBeInTheDocument())
    expect(screen.getByText(/no identity configuration/i)).toBeInTheDocument()
    expect(screen.getByText('AUTH_ISSUER')).toBeInTheDocument()
    expect(screen.getByText('CORE_CONSOLE_TOKEN_URL')).toBeInTheDocument()
    expect(screen.getByText('CORE_CONSOLE_CLIENT_ID')).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })

  /**
   * The other half of the same decision, and the reason the mode is not called
   * "guard-open": with no identity configured the console renders and CALLS, and
   * a dev-open core serves it. Nothing here concludes anything about the server
   * until the server answers.
   */
  it('renders the app and calls the API when nothing is refused', async () => {
    identity.value = null
    const okAdapter: AxiosAdapter = async (config) =>
      ({ data: { success: true, data: null }, status: 200, statusText: '', headers: {}, config }) as AxiosResponse

    function OpenProbe() {
      useEffect(() => {
        void apiClient.get('/tenants', { adapter: okAdapter }).catch(() => undefined)
      }, [])
      return <div>dashboard</div>
    }

    await renderGated(<OpenProbe />)

    await waitFor(() => expect(screen.getByText('dashboard')).toBeInTheDocument())
    expect(assign).not.toHaveBeenCalled()
    expect(screen.queryByText(/sign-in unavailable/i)).not.toBeInTheDocument()
  })
})

describe('an anonymous visit', () => {
  it('routes to /login once rather than rendering a shell of failed requests', async () => {
    await renderGated(<Probe />, '/tenants')

    // No token and a declined silent login: the guard hands off to the router,
    // so the protected content never mounts and no request is made at all.
    await waitFor(() => expect(screen.getByText('login page')).toBeInTheDocument())
    expect(screen.queryByText('dashboard')).not.toBeInTheDocument()
    // The guard hands off to /login; the navigation to identity is that page's
    // job, so nothing may leave from here.
    expect(assign).not.toHaveBeenCalled()
  })
})
