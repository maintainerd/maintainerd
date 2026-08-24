import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router-dom'
import { BrandLogo } from '@/components/brand/BrandMark'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { FormInputField, FormPasswordField } from '@/components/form'
import FormSubmitButton from '@/components/form/FormSubmitButton'
import { useRunSetup } from '@/hooks/useSetup'
import { useToast } from '@/hooks/useToast'
import { isSetupTokenRejected, SETUP_TOKEN_REJECTED_MESSAGE } from '@/services/api/setup'

interface FormValues {
  setup_token: string
  tenant_name: string
  tenant_display_name: string
  admin_username: string
  admin_fullname: string
  admin_email: string
  admin_password: string
  confirm_password: string
}

/**
 * First-run wizard. Collects the system tenant + the admin, then triggers Core's
 * orchestration (Core drives Auth's gRPC setup, registers itself as the control
 * service, and seeds the service registry) — everything is configured from here.
 *
 * IT ALSO COLLECTS THE BOOTSTRAP TOKEN. The endpoint it posts to is the one route
 * core's bearer guard cannot cover — it provisions the Auth that would mint the
 * bearer — so it carries its own shared-secret gate, core's `CORE_SETUP_TOKEN`.
 * The operator sets that on core and types the same value here, which is the
 * pattern maintainerd-auth and maintainerd-secret already use for their wizards.
 *
 * THE TOKEN LIVES AND DIES WITH THIS FORM. It is form state handed straight to the
 * request as a header — never storage, never a URL, never a log — and this
 * component only mounts before setup is complete: `SetupGate` sends /setup to
 * /dashboard once core reports `completed`, so there is no state in which the
 * field is on screen and the token is no longer wanted.
 */
export default function SetupPage() {
  const navigate = useNavigate()
  const run = useRunSetup()
  const { showSuccess, showError } = useToast()

  const {
    register,
    handleSubmit,
    setError,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: {
      setup_token: '',
      tenant_name: 'maintainerd',
      tenant_display_name: 'Maintainerd',
      admin_username: 'admin',
      admin_fullname: '',
      admin_email: '',
      admin_password: '',
      confirm_password: '',
    },
    mode: 'onTouched',
  })

  const onSubmit = async (v: FormValues) => {
    try {
      await run.mutateAsync({
        // An untouched field is "", which must mean "no token" rather than "an
        // empty token" — the gate compares in constant time and would read the
        // latter as a refusal instead of an unauthenticated first run.
        setupToken: v.setup_token || undefined,
        body: {
          tenant_name: v.tenant_name,
          tenant_display_name: v.tenant_display_name || undefined,
          admin_username: v.admin_username,
          admin_fullname: v.admin_fullname || undefined,
          admin_email: v.admin_email,
          admin_password: v.admin_password,
        },
      })
      showSuccess('Setup complete — everything is configured')
      navigate('/dashboard', { replace: true })
    } catch (e) {
      // A refused token is reported ON the token field. The raw envelope in a
      // toast ("missing or invalid setup token") reads as a permissions problem
      // with the console and gives no indication that the one field the operator
      // can act on is the one that is wrong.
      if (isSetupTokenRejected(e)) {
        setError('setup_token', { type: 'server', message: SETUP_TOKEN_REJECTED_MESSAGE })
        return
      }
      showError(e)
    } finally {
      // react-query keeps a SETTLED mutation's `variables` in its cache for the
      // default gcTime, and those variables carry the setup token. Drop them the
      // moment the call is over: the token's lifetime is this submit, not the tab's.
      run.reset()
    }
  }

  return (
    <div className="flex min-h-svh items-center justify-center bg-background px-4 py-10">
      <div className="w-full max-w-lg space-y-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <BrandLogo size={44} />
          <h1 className="text-2xl font-semibold tracking-tight">Set up maintainerd</h1>
          <p className="max-w-md text-sm text-muted-foreground">
            Create the system tenant and your administrator. Core configures the rest —
            Auth (IAM), Secret and Docker — behind the scenes.
          </p>
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          {/* OPTIONAL, AND THAT IS THE WHOLE POINT.

              Core is the starting point of an install: at first boot nothing
              exists — no auth, no tenant, no admin — so there is nowhere a
              credential could have come from. Requiring one here would lock the
              front door with the key inside.

              Core's gate matches that: an empty CORE_SETUP_TOKEN leaves the
              surface open in development, and DISABLES the trigger outside it, so
              an internet-reachable core is fail-closed rather than anonymous. The
              field exists for the operator who did set a token — either to harden
              a local stack, or because they are re-driving setup on a deployed
              instance — and it must never block the common first run. */}
          <Card>
            <CardHeader>
              <CardTitle className="text-base font-semibold">Authorize this install</CardTitle>
            </CardHeader>
            <CardContent className="space-y-5">
              {/* FormPasswordField, not FormInputField: it masks the value and
                  defaults autoComplete to "new-password", so no password manager
                  offers to remember a provisioning credential. */}
              <FormPasswordField
                label="Setup token"
                spellCheck={false}
                description="Only if core was started with CORE_SETUP_TOKEN. Leave this empty otherwise — a fresh install has no token. It authorizes this one call and is never stored by the console."
                error={errors.setup_token?.message}
                {...register('setup_token')}
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-semibold">System tenant</CardTitle>
            </CardHeader>
            <CardContent className="space-y-5">
              <FormInputField
                label="Tenant name"
                required
                placeholder="maintainerd"
                description="Stable machine name for the root tenant."
                error={errors.tenant_name?.message}
                {...register('tenant_name', { required: 'Tenant name is required' })}
              />
              <FormInputField
                label="Display name"
                placeholder="Maintainerd"
                error={errors.tenant_display_name?.message}
                {...register('tenant_display_name')}
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-semibold">Administrator</CardTitle>
            </CardHeader>
            <CardContent className="space-y-5">
              <FormInputField
                label="Username"
                required
                placeholder="admin"
                error={errors.admin_username?.message}
                {...register('admin_username', { required: 'Username is required' })}
              />
              <FormInputField
                label="Full name"
                placeholder="Ada Lovelace"
                error={errors.admin_fullname?.message}
                {...register('admin_fullname')}
              />
              <FormInputField
                label="Email"
                type="email"
                required
                placeholder="admin@example.com"
                error={errors.admin_email?.message}
                {...register('admin_email', { required: 'Email is required' })}
              />
              <FormPasswordField
                label="Password"
                required
                description="At least 12 characters."
                error={errors.admin_password?.message}
                {...register('admin_password', {
                  required: 'Password is required',
                  minLength: { value: 12, message: 'At least 12 characters' },
                })}
              />
              <FormPasswordField
                label="Confirm password"
                required
                error={errors.confirm_password?.message}
                {...register('confirm_password', {
                  validate: (v) => v === watch('admin_password') || 'Passwords do not match',
                })}
              />
            </CardContent>
          </Card>

          <div className="flex justify-end pt-2">
            <FormSubmitButton isSubmitting={isSubmitting} submitText="Complete setup" submittingText="Configuring…" />
          </div>
        </form>
      </div>
    </div>
  )
}
