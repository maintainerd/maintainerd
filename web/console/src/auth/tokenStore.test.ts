import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  clearAccessToken,
  getAccessToken,
  getIdTokenHint,
  hasAccessToken,
  setAccessToken,
  setIdTokenHint,
  subscribeToToken,
} from './tokenStore'

const TOKEN = 'eyJhbGciOiJSUzI1NiJ9.core-control-plane-access-token.signature'

beforeEach(() => {
  clearAccessToken()
  window.localStorage.clear()
  window.sessionStorage.clear()
})

afterEach(() => {
  clearAccessToken()
  vi.useRealTimers()
})

describe('the in-memory token store', () => {
  it('holds and returns the token', () => {
    setAccessToken(TOKEN)
    expect(getAccessToken()).toBe(TOKEN)
    expect(hasAccessToken()).toBe(true)
  })

  it('reports no token before one is set and after it is cleared', () => {
    expect(getAccessToken()).toBeNull()
    setAccessToken(TOKEN)
    clearAccessToken()
    expect(getAccessToken()).toBeNull()
    expect(hasAccessToken()).toBe(false)
  })

  it('drops the token 30 seconds before the server says it expires', () => {
    vi.useFakeTimers()
    setAccessToken(TOKEN, 60)
    expect(getAccessToken()).toBe(TOKEN)
    // 29s in: still inside the margin.
    vi.advanceTimersByTime(29_000)
    expect(getAccessToken()).toBe(TOKEN)
    // 31s in: the 30s safety margin has been reached, so the token is treated as
    // gone rather than being sent on a request that would 401 mid-flight.
    vi.advanceTimersByTime(2_000)
    expect(getAccessToken()).toBeNull()
  })

  it('keeps a token with no expires_in until it is cleared', () => {
    vi.useFakeTimers()
    setAccessToken(TOKEN)
    vi.advanceTimersByTime(86_400_000)
    expect(getAccessToken()).toBe(TOKEN)
  })

  it('notifies subscribers when the token is set and cleared', () => {
    const listener = vi.fn()
    const unsubscribe = subscribeToToken(listener)
    setAccessToken(TOKEN)
    clearAccessToken()
    expect(listener).toHaveBeenCalledTimes(2)
    unsubscribe()
    setAccessToken(TOKEN)
    expect(listener).toHaveBeenCalledTimes(2)
  })

  it('clears the id_token hint along with the access token', () => {
    setIdTokenHint('id-token-value')
    expect(getIdTokenHint()).toBe('id-token-value')
    clearAccessToken()
    expect(getIdTokenHint()).toBeNull()
  })
})

/**
 * The load-bearing property of this module.
 *
 * A token minted for core opens the whole control plane. If any of these fail,
 * an XSS in this console can walk away with a credential that outlives the tab
 * it was stolen from — which is the entire reason the store is in memory.
 */
describe('the token never reaches a storage API', () => {
  it('calls no storage setter while the token is set, read and cleared', () => {
    const localSet = vi.spyOn(Storage.prototype, 'setItem')
    const localRemove = vi.spyOn(Storage.prototype, 'removeItem')

    setAccessToken(TOKEN, 3600)
    setIdTokenHint('id-token-value')
    getAccessToken()
    clearAccessToken()

    expect(localSet).not.toHaveBeenCalled()
    expect(localRemove).not.toHaveBeenCalled()
    localSet.mockRestore()
    localRemove.mockRestore()
  })

  it('leaves both web storages empty', () => {
    setAccessToken(TOKEN, 3600)
    setIdTokenHint('id-token-value')

    expect(window.localStorage.length).toBe(0)
    expect(window.sessionStorage.length).toBe(0)
  })

  // A belt-and-braces scan: even a key written under an innocuous name would be
  // caught here, because the assertion is about the VALUE.
  it('puts the token nowhere a later reader could find it', () => {
    setAccessToken(TOKEN, 3600)
    setIdTokenHint('id-token-value')

    for (const storage of [window.localStorage, window.sessionStorage]) {
      for (let i = 0; i < storage.length; i += 1) {
        const key = storage.key(i)
        expect(storage.getItem(key ?? '')).not.toContain(TOKEN)
      }
    }
    expect(document.cookie).not.toContain(TOKEN)
  })
})
