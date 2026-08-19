import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Controller, useForm } from 'react-hook-form'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { FormInputField, FormSelectField, FormTextareaField } from '@/components/form'
import FormSubmitButton from '@/components/form/FormSubmitButton'
import { TENANT_STATUS_OPTIONS } from '@/lib/coreOptions'
import { parseJsonObject, stringifyJson } from '@/lib/json'
import { useTenant, useCreateTenant, useUpdateTenant } from '@/hooks/useTenants'
import { useToast } from '@/hooks/useToast'

interface FormValues {
  name: string
  display_name: string
  status: string
  auth_tenant_uuid: string
  metadata: string
}

export default function TenantForm() {
  const navigate = useNavigate()
  const { id } = useParams<{ id: string }>()
  const isEditing = Boolean(id)
  const { showSuccess, showError } = useToast()

  const { data: tenant, isLoading } = useTenant(id)
  const createM = useCreateTenant()
  const updateM = useUpdateTenant()

  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: { name: '', display_name: '', status: 'active', auth_tenant_uuid: '', metadata: '' },
    mode: 'onTouched',
  })

  useEffect(() => {
    if (isEditing && tenant) {
      reset({
        name: tenant.name,
        display_name: tenant.display_name ?? '',
        status: tenant.status,
        auth_tenant_uuid: tenant.auth_tenant_uuid ?? '',
        metadata: stringifyJson(tenant.metadata),
      })
    }
  }, [isEditing, tenant, reset])

  const onSubmit = async (values: FormValues) => {
    try {
      const metadata = parseJsonObject(values.metadata, 'Metadata')
      if (isEditing && id) {
        await updateM.mutateAsync({
          id,
          data: {
            display_name: values.display_name || undefined,
            status: values.status,
            auth_tenant_uuid: values.auth_tenant_uuid || undefined,
            metadata,
          },
        })
        showSuccess('Tenant updated')
      } else {
        await createM.mutateAsync({
          name: values.name,
          display_name: values.display_name || undefined,
          status: values.status,
          auth_tenant_uuid: values.auth_tenant_uuid || undefined,
          metadata,
        })
        showSuccess('Tenant created')
      }
      navigate('/tenants')
    } catch (e) {
      showError(e)
    }
  }

  if (isEditing && isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl="/tenants"
        backLabel="Back to tenants"
        title={isEditing ? 'Edit tenant' : 'New tenant'}
        description={isEditing ? 'Update this tenant’s settings.' : 'Create a new isolation boundary.'}
      />
      <form onSubmit={handleSubmit(onSubmit)} className="mt-6 space-y-6">
        <Card>
          <CardHeader>
            <CardTitle className="text-base font-semibold">Details</CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            <FormInputField
              label="Name"
              required
              placeholder="acme"
              description="Stable machine name. Cannot be changed after creation."
              disabled={isEditing}
              error={errors.name?.message}
              {...register('name', { required: 'Name is required' })}
            />
            <FormInputField
              label="Display name"
              placeholder="Acme Corporation"
              error={errors.display_name?.message}
              {...register('display_name')}
            />
            <Controller
              name="status"
              control={control}
              render={({ field }) => (
                <FormSelectField
                  label="Status"
                  options={TENANT_STATUS_OPTIONS}
                  value={field.value}
                  onValueChange={field.onChange}
                  error={errors.status?.message}
                  required
                />
              )}
            />
            <FormInputField
              label="Auth tenant UUID"
              placeholder="Optional — the system-Auth tenant this maps to"
              error={errors.auth_tenant_uuid?.message}
              {...register('auth_tenant_uuid')}
            />
            <FormTextareaField
              label="Metadata (JSON)"
              placeholder='{ "team": "platform" }'
              rows={5}
              className="font-mono text-xs"
              error={errors.metadata?.message}
              {...register('metadata')}
            />
          </CardContent>
        </Card>
        <div className="flex justify-end gap-3">
          <FormSubmitButton isSubmitting={isSubmitting} submitText={isEditing ? 'Update tenant' : 'Create tenant'} />
        </div>
      </form>
    </DetailsContainer>
  )
}
