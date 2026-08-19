import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Controller, useForm } from 'react-hook-form'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { FormInputField, FormSelectField, FormTextareaField } from '@/components/form'
import FormSubmitButton from '@/components/form/FormSubmitButton'
import { LIFECYCLE_STATUS_OPTIONS } from '@/lib/coreOptions'
import { parseJsonObject, stringifyJson } from '@/lib/json'
import { useService, useCreateService, useUpdateService } from '@/hooks/useServices'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

interface FormValues {
  name: string
  kind: string
  endpoint: string
  version: string
  status: string
  metadata: string
}

export default function ServiceForm() {
  const navigate = useNavigate()
  const { id } = useParams<{ id: string }>()
  const isEditing = Boolean(id)
  const { tenant } = useCoreTenant()
  const { showSuccess, showError } = useToast()

  const { data: service, isLoading } = useService(id)
  const createM = useCreateService()
  const updateM = useUpdateService()

  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: { name: '', kind: '', endpoint: '', version: '', status: 'active', metadata: '' },
    mode: 'onTouched',
  })

  useEffect(() => {
    if (isEditing && service) {
      reset({
        name: service.name,
        kind: service.kind,
        endpoint: service.endpoint ?? '',
        version: service.version ?? '',
        status: service.status,
        metadata: stringifyJson(service.metadata),
      })
    }
  }, [isEditing, service, reset])

  const onSubmit = async (values: FormValues) => {
    try {
      if (isEditing && id) {
        // The API only allows status + endpoint to change on a service.
        await updateM.mutateAsync({ id, data: { status: values.status, endpoint: values.endpoint || undefined } })
        showSuccess('Service updated')
      } else {
        if (!tenant) {
          showError(new Error('Select a tenant first.'))
          return
        }
        const metadata = parseJsonObject(values.metadata, 'Metadata')
        await createM.mutateAsync({
          tenant_uuid: tenant.tenant_uuid,
          name: values.name,
          kind: values.kind,
          endpoint: values.endpoint || undefined,
          version: values.version || undefined,
          metadata,
        })
        showSuccess('Service registered')
      }
      navigate('/services')
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
        backUrl="/services"
        backLabel="Back to services"
        title={isEditing ? 'Edit service' : 'Register service'}
        description={isEditing ? 'Only status and endpoint can be changed.' : 'Register a service under this tenant.'}
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
              placeholder="storage"
              disabled={isEditing}
              error={errors.name?.message}
              {...register('name', { required: 'Name is required' })}
            />
            <FormInputField
              label="Kind"
              required
              placeholder="object-store"
              disabled={isEditing}
              error={errors.kind?.message}
              {...register('kind', { required: 'Kind is required' })}
            />
            <FormInputField label="Endpoint" placeholder="https://storage.internal:9000" error={errors.endpoint?.message} {...register('endpoint')} />
            {!isEditing && (
              <FormInputField label="Version" placeholder="v1" error={errors.version?.message} {...register('version')} />
            )}
            {isEditing && (
              <Controller
                name="status"
                control={control}
                render={({ field }) => (
                  <FormSelectField
                    label="Status"
                    options={LIFECYCLE_STATUS_OPTIONS}
                    value={field.value}
                    onValueChange={field.onChange}
                    error={errors.status?.message}
                  />
                )}
              />
            )}
            {!isEditing && (
              <FormTextareaField
                label="Metadata (JSON)"
                rows={4}
                className="font-mono text-xs"
                error={errors.metadata?.message}
                {...register('metadata')}
              />
            )}
          </CardContent>
        </Card>
        <div className="flex justify-end gap-3">
          <FormSubmitButton isSubmitting={isSubmitting} submitText={isEditing ? 'Update service' : 'Register service'} />
        </div>
      </form>
    </DetailsContainer>
  )
}
