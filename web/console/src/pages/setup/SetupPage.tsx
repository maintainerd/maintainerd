import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router-dom'
import { Boxes } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { FormInputField, FormPasswordField } from '@/components/form'
import FormSubmitButton from '@/components/form/FormSubmitButton'
import { useRunSetup } from '@/hooks/useSetup'
import { useToast } from '@/hooks/useToast'

interface FormValues {
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
 */
export default function SetupPage() {
  const navigate = useNavigate()
  const run = useRunSetup()
  const { showSuccess, showError } = useToast()

  const {
    register,
    handleSubmit,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: {
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
        tenant_name: v.tenant_name,
        tenant_display_name: v.tenant_display_name || undefined,
        admin_username: v.admin_username,
        admin_fullname: v.admin_fullname || undefined,
        admin_email: v.admin_email,
        admin_password: v.admin_password,
      })
      showSuccess('Setup complete — everything is configured')
      navigate('/dashboard', { replace: true })
    } catch (e) {
      showError(e)
    }
  }

  return (
    <div className="flex min-h-svh items-center justify-center bg-background px-4 py-10">
      <div className="w-full max-w-lg space-y-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <Boxes className="size-10 text-foreground" />
          <h1 className="text-2xl font-semibold tracking-tight">Set up maintainerd</h1>
          <p className="max-w-md text-sm text-muted-foreground">
            Create the system tenant and your administrator. Core configures the rest —
            Auth (IAM), Secret and Docker — behind the scenes.
          </p>
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
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
