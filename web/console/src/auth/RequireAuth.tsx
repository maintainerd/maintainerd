import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from './authContext'
import { AuthBlockedScreen } from './AuthBlockedScreen'

/**
 * Gate for every protected route.
 *
 * An unauthenticated visit routes to /login rather than rendering a shell full
 * of failed requests. A 403 is NOT handled here on purpose: a forbidden call
 * means the caller IS authenticated and simply lacks that grant, so bouncing it
 * to sign-in would loop them through identity forever and still land on the same
 * refusal. Those surface in place, on the page that made the call.
 *
 * `identity-missing` RENDERS THE APP. It is not an authorization decision — this
 * console cannot know whether core's guard is enforced (core publishes no
 * capability endpoint), so the requests themselves are the probe. If the guard is
 * dev-open they succeed and the operator works normally; if it is enforced the
 * first 401 moves the mode to `blocked` and the branch below takes over.
 *
 * `blocked` renders IN PLACE rather than navigating. It is the end of the line,
 * and a redirect to a dedicated route would put a second navigation into exactly
 * the situation where navigations have stopped being productive.
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { mode } = useAuth()
  const location = useLocation()

  if (mode === 'blocked') return <AuthBlockedScreen />

  if (mode === 'anonymous') {
    return (
      <Navigate to="/login" replace state={{ from: `${location.pathname}${location.search}` }} />
    )
  }

  return <>{children}</>
}
