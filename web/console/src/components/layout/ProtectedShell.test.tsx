import type { ReactNode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext, type AuthContextValue, type AuthMode } from '@/auth/authContext'
import { createTestQueryClient } from '@/test/utils'
import { ProtectedShell } from './ProtectedShell'

// Mocked at the API-module boundary rather than at the hook, so what these tests
// assert is the thing that actually matters: whether a REQUEST leaves the app
// before auth has resolved. Core's guard is moving from dev-open to enforced, and
// a call fired above the gate stops being a wasted round-trip and becomes a 401
// that knocks the console into its blocked state on boot.
const listTenants = vi.hoisted(() => vi.fn())
const getSetupStatus = vi.hoisted(() => vi.fn())

vi.mock('@/services/api/tenants', () => ({ listTenants }))
vi.mock('@/services/api/setup', () => ({ getSetupStatus, runSetup: vi.fn() }))

function authValue(mode: AuthMode): AuthContextValue {
  return {
    mode,
    ready: true,
    identity: null,
    blockedReason: mode === 'blocked' ? 'identity-missing' : null,
    signIn: vi.fn(),
    signOut: vi.fn(),
  }
}

function renderShell(mode: AuthMode, children: ReactNode = <div>dashboard</div>) {
  return render(
    <QueryClientProvider client={createTestQueryClient()}>
      <AuthContext.Provider value={authValue(mode)}>
        <MemoryRouter initialEntries={['/dashboard']}>
          <Routes>
            <Route path="/login" element={<div>login page</div>} />
            <Route path="/setup" element={<div>setup wizard</div>} />
            <Route element={<ProtectedShell />}>
              <Route path="/dashboard" element={children} />
            </Route>
          </Routes>
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  listTenants.mockReset()
  getSetupStatus.mockReset()
  listTenants.mockResolvedValue({ items: [], total: 0 })
  getSetupStatus.mockResolvedValue({ completed: true })
})

describe('ProtectedShell gate ordering', () => {
  it('redirects an anonymous visitor to /login', async () => {
    renderShell('anonymous')
    expect(await screen.findByText('login page')).toBeInTheDocument()
    expect(screen.queryByText('dashboard')).not.toBeInTheDocument()
  })

  // The regression this component was created for: CoreTenantProvider lists
  // tenants the moment it mounts, and it used to mount above the auth gate.
  it('fires no tenant list call for an anonymous visitor', async () => {
    renderShell('anonymous')
    await screen.findByText('login page')
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(listTenants).not.toHaveBeenCalled()
  })

  it('fires no setup status call for an anonymous visitor', async () => {
    renderShell('anonymous')
    await screen.findByText('login page')
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(getSetupStatus).not.toHaveBeenCalled()
  })

  it('renders the blocked screen without calling the API', async () => {
    renderShell('blocked')
    expect(await screen.findByText('Sign-in unavailable')).toBeInTheDocument()
    expect(listTenants).not.toHaveBeenCalled()
    expect(getSetupStatus).not.toHaveBeenCalled()
  })

  // The mirror assertion. Without it the three above would still pass if the
  // provider had simply been unmounted, which would be a different bug.
  it('lists tenants once auth has resolved and setup is complete', async () => {
    renderShell('authenticated')
    expect(await screen.findByText('dashboard')).toBeInTheDocument()
    await waitFor(() => expect(listTenants).toHaveBeenCalled())
  })

  // `identity-missing` renders the app on purpose: the console cannot know
  // whether core's guard is enforced, so the request is the probe. Asserted so
  // that intent stays deliberate rather than becoming an accident.
  it('renders and probes in identity-missing mode', async () => {
    renderShell('identity-missing')
    expect(await screen.findByText('dashboard')).toBeInTheDocument()
    await waitFor(() => expect(listTenants).toHaveBeenCalled())
  })

  it('sends an authenticated visitor to /setup when the control plane is unprovisioned', async () => {
    getSetupStatus.mockResolvedValue({ completed: false })
    renderShell('authenticated')
    expect(await screen.findByText('setup wizard')).toBeInTheDocument()
    expect(listTenants).not.toHaveBeenCalled()
  })

  // Fails closed: an unreadable status must land on the wizard, never render the
  // console as though the control plane were provisioned. The generous timeout is
  // useSetupStatus's own `retry: 1` — it overrides the test client's retry:false,
  // so the verdict is one retry and its backoff away, not immediate.
  it('sends an authenticated visitor to /setup when the status cannot be read', async () => {
    getSetupStatus.mockRejectedValue(new Error('core unreachable'))
    renderShell('authenticated')
    expect(await screen.findByText('setup wizard', {}, { timeout: 5000 })).toBeInTheDocument()
    expect(listTenants).not.toHaveBeenCalled()
  })
})
