import { lazy, type ComponentType } from 'react'
import { render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext, type AuthContextValue } from '@/auth/authContext'
import { CoreTenantProvider } from '@/context/CoreTenantContext'
import { createTestQueryClient } from '@/test/utils'
import { PrivateLayout } from './PrivateLayout'

const listTenants = vi.hoisted(() => vi.fn())
vi.mock('@/services/api/tenants', () => ({ listTenants }))

const AUTH: AuthContextValue = {
  mode: 'authenticated',
  ready: true,
  identity: null,
  blockedReason: null,
  signIn: vi.fn(),
  signOut: vi.fn(),
}

// A route chunk whose arrival this test controls, standing in for React.lazy on a
// cold cache — the condition under which the nav used to freeze.
function makePendingRoute() {
  let release!: (value: { default: ComponentType }) => void
  const chunk = new Promise<{ default: ComponentType }>((resolve) => {
    release = resolve
  })
  return { Page: lazy(() => chunk), release }
}

function renderLayout(Page: ComponentType) {
  return render(
    <QueryClientProvider client={createTestQueryClient()}>
      <AuthContext.Provider value={AUTH}>
        <CoreTenantProvider>
          <MemoryRouter initialEntries={['/dashboard']}>
            <Routes>
              <Route element={<PrivateLayout fullWidth />}>
                <Route path="/dashboard" element={<Page />} />
              </Route>
            </Routes>
          </MemoryRouter>
        </CoreTenantProvider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  listTenants.mockReset()
  listTenants.mockResolvedValue({ items: [], total: 0 })
})

describe('PrivateLayout route suspense boundary', () => {
  // The operator-visible symptom this guards: with the only Suspense boundary
  // above <Routes>, a pending lazy chunk replaced the whole app — sidebar and
  // brand bar included — so the nav appeared to freeze after a click landed.
  it('keeps the sidebar and brand bar mounted while a route chunk is pending', async () => {
    const { Page } = makePendingRoute()
    renderLayout(Page)

    expect(await screen.findByText('Loading page')).toBeInTheDocument()

    // The shell is still there behind the content-region fallback.
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /tenants/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /toggle sidebar/i })).toBeInTheDocument()
  })

  it('scopes the fallback to the content region, not the viewport', async () => {
    const { Page } = makePendingRoute()
    renderLayout(Page)

    const fallback = await screen.findByText('Loading page')
    // Inside <main>, which is what keeps the sidebar visible alongside it.
    expect(fallback.closest('main')).not.toBeNull()
  })

  it('swaps the fallback for the page once the chunk resolves', async () => {
    const { Page, release } = makePendingRoute()
    renderLayout(Page)

    await screen.findByText('Loading page')
    release({ default: () => <h1>Dashboard content</h1> })

    expect(await screen.findByText('Dashboard content')).toBeInTheDocument()
    expect(screen.queryByText('Loading page')).not.toBeInTheDocument()
    // Shell survived the swap rather than remounting around it.
    expect(screen.getByRole('link', { name: /tenants/i })).toBeInTheDocument()
  })

  it('marks the active route on the sidebar while the chunk is still pending', async () => {
    const { Page } = makePendingRoute()
    renderLayout(Page)

    await screen.findByText('Loading page')
    expect(screen.getByRole('link', { name: /dashboard/i })).toHaveAttribute(
      'data-active',
      'true',
    )
  })
})
