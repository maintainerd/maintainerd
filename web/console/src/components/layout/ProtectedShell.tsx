import { Outlet } from 'react-router-dom'
import { RequireAuth } from '@/auth/RequireAuth'
import { SetupGate } from '@/components/SetupGate'
import { CoreTenantProvider } from '@/context/CoreTenantContext'

/**
 * The gates every signed-in route passes through, in order.
 *
 *   RequireAuth         keeps unauthenticated visitors off the app
 *   SetupGate           keeps everyone off an unprovisioned control plane
 *   CoreTenantProvider  owns the active tenant every listing is scoped to
 *
 * THE ORDER IS THE POINT. CoreTenantProvider lists tenants the moment it mounts,
 * and that call is authenticated — mounting it above the auth gate is what made
 * the console fire a doomed request before it knew whether it held a credential.
 * SetupGate's status call is the exception that proves the rule: core exempts the
 * `setup` path segment from its guard, so it is safe on either side.
 *
 * It exists as a layout route rendering `<Outlet/>` so the gates are declared
 * ONCE and the content layout nests beneath it, instead of remounting the tenant
 * provider and re-running the setup check per layout.
 */
export function ProtectedShell() {
  return (
    <RequireAuth>
      <SetupGate>
        <CoreTenantProvider>
          <Outlet />
        </CoreTenantProvider>
      </SetupGate>
    </RequireAuth>
  )
}
