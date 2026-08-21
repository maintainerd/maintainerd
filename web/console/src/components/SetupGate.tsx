import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import AppLoadingScreen from '@/components/layout/AppLoadingScreen'
import { useSetupStatus } from '@/hooks/useSetup'

/**
 * First-run gate. While the setup status loads, shows the splash. If Core has
 * not been set up, everything redirects to /setup; once set up, /setup bounces
 * to the dashboard. Fails CLOSED (treats the install as not set up) when the
 * status can't be read: an error must never render the full console as if the
 * control plane were provisioned — the wizard is the safe landing surface,
 * and it recovers on its own once the status endpoint responds.
 */
export function SetupGate({ children }: { children: ReactNode }) {
  const { data, isLoading } = useSetupStatus()
  const location = useLocation()

  if (isLoading) return <AppLoadingScreen />

  const completed = data?.completed ?? false // fail closed on error/unknown
  const onSetup = location.pathname === '/setup'

  if (!completed && !onSetup) return <Navigate to="/setup" replace />
  if (completed && onSetup) return <Navigate to="/dashboard" replace />

  return <>{children}</>
}
