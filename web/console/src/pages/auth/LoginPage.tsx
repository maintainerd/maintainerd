import { useEffect, useRef } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { BrandMark } from '@/components/brand/BrandMark'
import { useAuth } from '@/auth/authContext'
import { AuthBlockedScreen } from '@/auth/AuthBlockedScreen'
import { POST_LOGIN_ROUTE, safeReturnTo } from '@/auth/oauthFlow'

/**
 * Sign-in.
 *
 * The redirect fires automatically — an administrative console has exactly one
 * way in, so making the operator click a button first is ceremony. The button is
 * still rendered as the fallback for a browser that blocked the navigation.
 *
 * `startedRef` keeps this to one attempt per mount; the persisted attempt budget
 * in `oauthFlow.ts` is what keeps it to a bounded number across the reloads a
 * loop would otherwise consist of.
 */
export default function LoginPage() {
  const { mode, signIn } = useAuth()
  const location = useLocation()
  const startedRef = useRef(false)

  const from = (location.state as { from?: string } | null)?.from
  const returnTo = safeReturnTo(from) ?? POST_LOGIN_ROUTE

  useEffect(() => {
    if (mode !== 'anonymous' || startedRef.current) return
    startedRef.current = true
    signIn(returnTo)
  }, [mode, signIn, returnTo])

  // Reached /login with nothing to sign in with, or with the budget spent. Say so
  // here instead of navigating on: this route is already the place people land
  // when auth is unresolved, so bouncing them elsewhere just moves the dead end.
  if (mode === 'blocked') return <AuthBlockedScreen />

  if (mode !== 'anonymous') return <Navigate to={returnTo} replace />

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-8 bg-background px-4 text-center">
      <div className="flex w-full max-w-sm flex-col items-center gap-6">
        <BrandMark size={32} showTag={false} />
        <div className="space-y-2">
          <h1 className="text-lg font-semibold tracking-tight">Sign in</h1>
          <p className="text-sm text-muted-foreground">
            Taking you to your identity provider. Nothing about your session is stored in this
            browser.
          </p>
        </div>
        <div
          className="flex items-center justify-center gap-2 text-muted-foreground"
          role="status"
          aria-live="polite"
        >
          <Loader2 className="size-4 animate-spin" aria-hidden="true" />
          <span className="text-sm">Redirecting…</span>
        </div>
        <Button className="w-full" onClick={() => signIn(returnTo)}>
          Continue to sign in
        </Button>
      </div>
    </div>
  )
}
