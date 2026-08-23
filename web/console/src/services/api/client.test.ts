import { AxiosError, type AxiosAdapter, type AxiosResponse, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiClient, setUnauthenticatedHandler } from './client'
import { clearAccessToken, getAccessToken, setAccessToken } from '@/auth/tokenStore'

const TOKEN = 'core-control-plane-access-token'

/**
 * A stand-in transport. Interceptors run around the adapter, so this exercises
 * the real bearer-attaching and 401-handling paths without a network or a new
 * dependency.
 */
function respondWith(status: number, data: unknown): AxiosAdapter {
  return async (config: InternalAxiosRequestConfig) => {
    const response = {
      data,
      status,
      statusText: '',
      headers: {},
      config,
    } as AxiosResponse
    if (status >= 200 && status < 300) return response
    throw new AxiosError('request failed', 'ERR_BAD_REQUEST', config, {}, response)
  }
}

const ok = respondWith(200, { success: true, data: { items: [], total: 0 } })
const unauthorized = respondWith(401, { success: false, error: 'missing bearer token' })

/** Captures the Authorization header the interceptor put on the request. */
function captureAuthHeader(): { adapter: AxiosAdapter; value: () => unknown } {
  let seen: unknown
  const adapter: AxiosAdapter = async (config) => {
    seen = config.headers?.Authorization
    return (await ok(config)) as AxiosResponse
  }
  return { adapter, value: () => seen }
}

beforeEach(() => {
  clearAccessToken()
  // Registering also resets the once-per-page latch, which is what makes each
  // test a fresh "page load".
  setUnauthenticatedHandler(null)
})

afterEach(() => {
  clearAccessToken()
  setUnauthenticatedHandler(null)
  vi.restoreAllMocks()
})

describe('the bearer token', () => {
  it('is attached when one is held', async () => {
    setAccessToken(TOKEN)
    const { adapter, value } = captureAuthHeader()
    await apiClient.get('/tenants', { adapter })
    expect(value()).toBe(`Bearer ${TOKEN}`)
  })

  // No token is a legitimate request to make: core's guard is dev-open in the
  // development stack and serves it. Sending `Bearer null` would not be.
  it('is omitted entirely when none is held', async () => {
    const { adapter, value } = captureAuthHeader()
    await apiClient.get('/tenants', { adapter })
    expect(value()).toBeUndefined()
  })

  // The token lives in memory and a silent re-authorization can replace it at any
  // moment, so it must be read per request rather than baked into the instance.
  it('is re-read on every request', async () => {
    const first = captureAuthHeader()
    setAccessToken('first-token')
    await apiClient.get('/tenants', { adapter: first.adapter })
    expect(first.value()).toBe('Bearer first-token')

    const second = captureAuthHeader()
    setAccessToken('second-token')
    await apiClient.get('/tenants', { adapter: second.adapter })
    expect(second.value()).toBe('Bearer second-token')
  })

  it('is attached to writes as well as reads', async () => {
    setAccessToken(TOKEN)
    const { adapter, value } = captureAuthHeader()
    await apiClient.post('/tenants', { name: 'acme' }, { adapter })
    expect(value()).toBe(`Bearer ${TOKEN}`)
  })
})

describe('a 401 response', () => {
  it('surfaces the backend message as an ApiError', async () => {
    await expect(apiClient.get('/tenants', { adapter: unauthorized })).rejects.toMatchObject({
      name: 'ApiError',
      status: 401,
      message: 'missing bearer token',
    })
  })

  it('clears the held token', async () => {
    setAccessToken(TOKEN)
    await expect(apiClient.get('/tenants', { adapter: unauthorized })).rejects.toThrow(ApiError)
    expect(getAccessToken()).toBeNull()
  })

  it('invokes the unauthenticated handler', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)
    await expect(apiClient.get('/tenants', { adapter: unauthorized })).rejects.toThrow(ApiError)
    expect(handler).toHaveBeenCalledTimes(1)
  })

  /**
   * THE LOOP TEST. The dashboard fans out — tenants, projects, services,
   * providers, agents — and on an enforced guard every one of them 401s at once.
   * Each is a separate response interceptor invocation, so without the latch each
   * would start its own navigation to the identity app and they would race,
   * clobbering one another's pending PKCE state.
   */
  it('triggers exactly one redirect for a fan-out of simultaneous 401s', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)

    const results = await Promise.allSettled([
      apiClient.get('/tenants', { adapter: unauthorized }),
      apiClient.get('/projects', { adapter: unauthorized }),
      apiClient.get('/services', { adapter: unauthorized }),
      apiClient.get('/providers', { adapter: unauthorized }),
      apiClient.get('/agents', { adapter: unauthorized }),
    ])

    // Every caller still learns its request failed — the latch suppresses the
    // navigation, never the error.
    expect(results.every((r) => r.status === 'rejected')).toBe(true)
    expect(handler).toHaveBeenCalledTimes(1)
  })

  // Same property across time rather than in parallel: a later 401 in the same
  // page load must not start a second sign-in on top of the one already running.
  it('does not re-trigger on subsequent 401s in the same page load', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)

    await expect(apiClient.get('/tenants', { adapter: unauthorized })).rejects.toThrow(ApiError)
    await expect(apiClient.get('/projects', { adapter: unauthorized })).rejects.toThrow(ApiError)
    await expect(apiClient.post('/tenants', {}, { adapter: unauthorized })).rejects.toThrow(ApiError)

    expect(handler).toHaveBeenCalledTimes(1)
  })

  // Bootstrap owns routing until AuthProvider registers. Redirecting on a verdict
  // that is still being determined is the classic reload loop.
  it('is reported without a redirect when no handler is registered', async () => {
    await expect(apiClient.get('/tenants', { adapter: unauthorized })).rejects.toMatchObject({
      status: 401,
    })
  })

  it('stops calling a handler that has been unregistered', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)
    setUnauthenticatedHandler(null)
    await expect(apiClient.get('/tenants', { adapter: unauthorized })).rejects.toThrow(ApiError)
    expect(handler).not.toHaveBeenCalled()
  })
})

describe('other failures', () => {
  // A 403 means the caller IS authenticated and lacks a grant. Treating it as a
  // session problem would bounce them through identity forever and land on the
  // same refusal.
  it('does not touch the token or the handler on a 403', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)
    setAccessToken(TOKEN)

    await expect(
      apiClient.get('/tenants', {
        adapter: respondWith(403, { success: false, error: 'requires permission core:tenant:read' }),
      }),
    ).rejects.toMatchObject({ status: 403 })

    expect(getAccessToken()).toBe(TOKEN)
    expect(handler).not.toHaveBeenCalled()
  })

  it('does not treat a 500 as a session problem', async () => {
    const handler = vi.fn()
    setUnauthenticatedHandler(handler)
    setAccessToken(TOKEN)

    await expect(
      apiClient.get('/tenants', { adapter: respondWith(500, { success: false }) }),
    ).rejects.toMatchObject({ status: 500 })

    expect(getAccessToken()).toBe(TOKEN)
    expect(handler).not.toHaveBeenCalled()
  })
})
