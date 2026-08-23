import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, get, post } from '../client'
import { SETUP_TOKEN_HEADER } from '../config'
import {
  getSetupStatus,
  isSetupTokenRejected,
  runSetup,
  SETUP_TOKEN_REJECTED_MESSAGE,
} from './index'

/**
 * The setup surface's credential handling.
 *
 * The VERBS are stubbed rather than the transport, because what has to be pinned
 * here is the contract with core's own gate: the exact header name it reads
 * (`setupTokenHeader` in internal/setup/handler.go), the token never reaching the
 * URL or the body, and the header being omitted rather than sent empty when there
 * is none. `ApiError` stays real so `isSetupTokenRejected` is exercised as the
 * page calls it. The other half of this fix — a 401 here not starting a
 * re-authorization — runs through the real interceptor in `../client.test.ts`.
 */
vi.mock('../client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../client')>()),
  get: vi.fn(),
  post: vi.fn(),
}))

const mockGet = vi.mocked(get)
const mockPost = vi.mocked(post)

const TOKEN = 'core-bootstrap-setup-token'

const body = {
  tenant_name: 'maintainerd',
  admin_username: 'admin',
  admin_email: 'admin@maintainerd.local',
  admin_password: 'a-sufficiently-long-password',
}

beforeEach(() => {
  mockGet.mockReset().mockResolvedValue({ success: true, data: { completed: false } })
  mockPost.mockReset().mockResolvedValue({ success: true, data: { already_complete: false } })
})

describe('the setup token', () => {
  it('is sent on the header core reads', async () => {
    await runSetup(body, TOKEN)

    const [, , config] = mockPost.mock.calls[0]
    expect(config?.headers).toEqual({ [SETUP_TOKEN_HEADER]: TOKEN })
  })

  // The gate compares a shared secret that provisions the first tenant and admin.
  // In a URL it would be in browser history, in the referer of everything that
  // followed, and in every proxy access log on the way to core.
  it('never appears in the URL', async () => {
    await runSetup(body, TOKEN)

    const [url] = mockPost.mock.calls[0]
    expect(url).toBe('/setup')
    expect(url).not.toContain(TOKEN)
  })

  // It authenticates the call; it is not part of what gets provisioned. In the body
  // it would be persisted by anything that logs request payloads.
  it('never appears in the request body', async () => {
    await runSetup(body, TOKEN)

    const [, sent] = mockPost.mock.calls[0]
    expect(sent).toEqual(body)
    expect(JSON.stringify(sent)).not.toContain(TOKEN)
  })

  it('widens the status read when one is supplied', async () => {
    await getSetupStatus(TOKEN)

    const [url, config] = mockGet.mock.calls[0]
    expect(url).toBe('/setup/status')
    expect(config?.headers).toEqual({ [SETUP_TOKEN_HEADER]: TOKEN })
  })

  // An empty header is not a credential the gate can compare, and the first-run
  // gate check only needs the anonymous `completed` bit.
  it('is omitted from the status read entirely when there is none', async () => {
    await getSetupStatus()

    const [, config] = mockGet.mock.calls[0]
    expect(config?.headers).toBeUndefined()
  })

  // Both calls opt out of session recovery: this surface holds no bearer of its
  // own, so its 401 must not clear one or navigate away from the wizard.
  it('opts both calls out of session recovery', async () => {
    await getSetupStatus()
    await runSetup(body, TOKEN)

    expect(mockGet.mock.calls[0][1]?.skipSessionRecovery).toBe(true)
    expect(mockPost.mock.calls[0][2]?.skipSessionRecovery).toBe(true)
  })
})

describe('a refused token', () => {
  it('is recognized from the 401 the gate answers with', async () => {
    mockPost.mockRejectedValue(
      new ApiError({ message: 'missing or invalid setup token', status: 401 }),
    )

    const failure = await runSetup(body, 'wrong-token').catch((e: unknown) => e)
    expect(isSetupTokenRejected(failure)).toBe(true)
  })

  // The operator's fix is a named variable on a named service. The server's own
  // "missing or invalid setup token" states the problem and not the remedy, and the
  // client's generic 401 text ("You are not authorized to perform this action")
  // points at the wrong thing entirely.
  it('is reported as a token problem naming the variable to set', () => {
    expect(SETUP_TOKEN_REJECTED_MESSAGE).toMatch(/setup token was refused/i)
    expect(SETUP_TOKEN_REJECTED_MESSAGE).toContain('CORE_SETUP_TOKEN')
  })

  // A 403 is core refusing because CORE_SETUP_TOKEN is UNSET, which disables the
  // surface outright — no token typed into the wizard can fix that, so it must not
  // be reported as a bad token.
  it('is not confused with the surface being disabled', () => {
    expect(
      isSetupTokenRejected(
        new ApiError({ message: 'setup is disabled: CORE_SETUP_TOKEN is not set', status: 403 }),
      ),
    ).toBe(false)
    expect(isSetupTokenRejected(new Error('network down'))).toBe(false)
  })
})
