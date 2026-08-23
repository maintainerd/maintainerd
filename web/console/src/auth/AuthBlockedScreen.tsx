import { ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { BrandMark } from '@/components/brand/BrandMark'
import { IDENTITY_SETTING_KEYS } from '@/services/api/config'
import { useAuth } from './authContext'
import { POST_LOGIN_ROUTE, clearSignInAttempts } from './oauthFlow'

/**
 * The terminal state: core refuses this console's requests and signing in is not
 * a remedy this console can apply.
 *
 * IT IS A DEAD END ON PURPOSE. Every other unauthenticated path in this app
 * moves — to identity, to /login, back to the page that was asked for. This one
 * stops, because both reasons it renders are configuration facts that no number
 * of redirects will change: there is no authorize endpoint to go to, or going
 * there already produced a token core would not accept. A screen an operator can
 * read and act on is the only correct output; a bounce is the bug.
 *
 * The remedies are named as the exact runtime keys, not described in prose. The
 * operator is looking at a deployment's config, so that is the vocabulary that
 * closes the gap.
 */
export function AuthBlockedScreen() {
  const { blockedReason, signIn } = useAuth()

  // A human clicking "try again" is itself the loop breaker: the budget is
  // cleared only on a deliberate press, so a genuinely broken configuration
  // costs one redirect per click instead of spinning unattended.
  const retry = () => {
    clearSignInAttempts()
    signIn(POST_LOGIN_ROUTE)
  }

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-8 bg-background px-4 text-center">
      <div className="flex w-full max-w-md flex-col items-center gap-6">
        <BrandMark size={32} showTag={false} />
        <div className="flex flex-col items-center gap-3">
          <div className="flex size-14 items-center justify-center rounded-full bg-destructive/10">
            <ShieldAlert className="size-7 text-destructive" />
          </div>
          <h1 className="text-2xl font-semibold tracking-tight">Sign-in unavailable</h1>
        </div>

        {blockedReason === 'identity-missing' ? (
          <Alert variant="destructive" className="text-left">
            <AlertTitle>This console has no identity configuration</AlertTitle>
            <AlertDescription>
              <p>
                The control plane requires a bearer token, and this console has not been told where
                to get one. Set these on the console deployment and reload:
              </p>
              <ul className="mt-2 list-disc space-y-1 pl-5 font-mono text-xs">
                {IDENTITY_SETTING_KEYS.map((key) => (
                  <li key={key}>{key}</li>
                ))}
              </ul>
              <p className="mt-2">
                <code className="font-mono text-xs">AUTH_AUDIENCE</code> must also match the
                audience the control plane enforces, or the token it mints will be refused.
              </p>
            </AlertDescription>
          </Alert>
        ) : (
          <Alert variant="destructive" className="text-left">
            <AlertTitle>Signing in did not give access</AlertTitle>
            <AlertDescription>
              <p>
                Sign-in completed but the control plane still refused the request, so it was stopped
                rather than retried indefinitely. This usually means the token was minted for a
                different audience than the control plane enforces, or the signed-in principal holds
                no permissions on it.
              </p>
              <p className="mt-2">
                Check that <code className="font-mono text-xs">AUTH_AUDIENCE</code> matches on both
                sides, then try again.
              </p>
            </AlertDescription>
          </Alert>
        )}

        {blockedReason === 'sign-in-failed' && <Button onClick={retry}>Try signing in again</Button>}
      </div>
    </div>
  )
}
