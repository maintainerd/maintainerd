import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { BrandMark } from '@/components/brand/BrandMark'
import AppLoadingScreen from '@/components/layout/AppLoadingScreen'
import { useAuth } from '@/auth/authContext'
import { exchangeAuthorizationCode } from '@/auth/oauthClient'
import {
  POST_LOGIN_ROUTE,
  consumePendingOAuthFlow,
  discardPendingOAuthFlow,
  safeReturnTo,
} from '@/auth/oauthFlow'

/**
 * The OAuth redirect target.
 *
 * The `state` comparison inside `consumePendingOAuthFlow` is the CSRF check: a
 * code that arrives without a matching pending flow is refused rather than
 * exchanged, and the removal that comparison performs is what makes the verifier
 * single-use. The code and state are dropped from the URL by navigating away with
 * `replace`, so neither survives in history.
 *
 * EVERY FAILURE PATH ENDS ON THIS PAGE, not in a redirect. This route is where a
 * broken sign-in becomes visible, and sending it back to /login would re-enter
 * the flow that just failed — which, with a live SSO session upstream, is a loop
 * that needs no user input to keep going. The button makes the retry deliberate.
 */
export default function OAuthCallbackPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { identity } = useAuth()
  const [error, setError] = useState<string | null>(null)
  const startedRef = useRef(false)

  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true

    const run = async () => {
      const code = params.get('code')
      const state = params.get('state')
      const oauthError = params.get('error')

      if (oauthError) {
        // The provider's own error code is not surfaced verbatim; it is chosen by
        // an upstream system and can carry request detail.
        if (state) discardPendingOAuthFlow(state)
        setError('Your identity provider refused the sign-in request.')
        return
      }
      if (!identity) {
        setError('This console has no identity configuration, so it cannot complete a sign-in.')
        return
      }
      if (!code || !state) {
        setError('The sign-in response was incomplete.')
        return
      }
      const flow = consumePendingOAuthFlow(state)
      if (!flow) {
        // Either the state did not match or the pending flow is gone (a reload of
        // the callback, or a second tab). Failing here is correct: exchanging a
        // code whose origin we cannot vouch for is the attack this check exists
        // to stop, and replaying a consumed verifier is the other one.
        setError('This sign-in link is no longer valid. Please sign in again.')
        return
      }
      try {
        await exchangeAuthorizationCode(identity, {
          code,
          redirectUri: flow.redirectUri,
          codeVerifier: flow.codeVerifier,
        })
        navigate(safeReturnTo(flow.returnTo) ?? POST_LOGIN_ROUTE, { replace: true })
      } catch {
        // The exchange failure is never surfaced or logged — the request carried
        // a verifier and the response may carry a token.
        setError('Could not complete sign-in. Please try again.')
      }
    }

    void run()
  }, [identity, navigate, params])

  if (error) {
    return (
      <div className="flex min-h-svh flex-col items-center justify-center gap-8 bg-background px-4">
        <div className="flex w-full max-w-md flex-col items-center gap-6">
          <BrandMark size={32} showTag={false} />
          <h1 className="text-lg font-semibold tracking-tight">Sign-in failed</h1>
          <Alert variant="destructive">
            <AlertTitle>Could not complete sign-in</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
          <Button className="w-full" onClick={() => navigate('/login', { replace: true })}>
            Try again
          </Button>
        </div>
      </div>
    )
  }

  return <AppLoadingScreen message="Completing sign-in" />
}
