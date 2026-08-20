import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listTenants } from '@/services/api/tenants'
import type { Tenant } from '@/services/api/tenants/types'

const STORAGE_KEY = 'maintainerd.console.tenant'

interface CoreTenantContextValue {
  /** The active tenant's UUID (the scope for projects/services/providers/agents). */
  tenantUuid?: string
  /** The active tenant object, if resolved. */
  tenant?: Tenant
  /** All tenants (for the switcher). */
  tenants: Tenant[]
  isLoading: boolean
  error: Error | null
  setTenantUuid: (uuid: string) => void
}

const CoreTenantContext = createContext<CoreTenantContextValue | undefined>(undefined)

/**
 * Holds the "active tenant" the console operates within. Nearly every core list
 * endpoint is tenant-scoped (`?tenant=`), so the switcher in the top bar picks
 * the scope once and every listing reads it from here. Defaults to the system
 * tenant, falling back to the first tenant; the choice is persisted per browser.
 */
export function CoreTenantProvider({ children }: { children: ReactNode }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['tenants', 'all-for-switcher'],
    queryFn: () => listTenants({ limit: 100 }),
    placeholderData: keepPreviousData,
  })

  const tenants = useMemo(() => data?.items ?? [], [data])

  const [selected, setSelected] = useState<string | undefined>(() => {
    if (typeof window === 'undefined') return undefined
    return window.localStorage.getItem(STORAGE_KEY) ?? undefined
  })

  // Resolve the effective tenant: the persisted choice if it still exists,
  // otherwise the system tenant, otherwise the first tenant.
  const tenant = useMemo(() => {
    if (!tenants.length) return undefined
    const chosen = selected && tenants.find((t) => t.tenant_uuid === selected)
    if (chosen) return chosen
    return tenants.find((t) => t.is_system) ?? tenants[0]
  }, [tenants, selected])

  // Keep the persisted value aligned with the resolved tenant.
  useEffect(() => {
    if (tenant && tenant.tenant_uuid !== selected) {
      setSelected(tenant.tenant_uuid)
      window.localStorage.setItem(STORAGE_KEY, tenant.tenant_uuid)
    }
  }, [tenant, selected])

  const setTenantUuid = (uuid: string) => {
    setSelected(uuid)
    window.localStorage.setItem(STORAGE_KEY, uuid)
  }

  const value: CoreTenantContextValue = {
    tenantUuid: tenant?.tenant_uuid,
    tenant,
    tenants,
    isLoading,
    error: (error as Error) ?? null,
    setTenantUuid,
  }

  return <CoreTenantContext.Provider value={value}>{children}</CoreTenantContext.Provider>
}

// Provider + hook are intentionally co-located; the hook is this file's only
// non-component export, so opt out of the dev-only fast-refresh lint rule here.
// eslint-disable-next-line react-refresh/only-export-components
export function useCoreTenant(): CoreTenantContextValue {
  const ctx = useContext(CoreTenantContext)
  if (!ctx) throw new Error('useCoreTenant must be used within a CoreTenantProvider')
  return ctx
}
