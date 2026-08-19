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
import { useProvider, useCreateProvider, useUpdateProvider } from '@/hooks/useProviders'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

interface FormValues {
  name: string
  resource_kind: string
  driver: string
  status: string
  config: string
  metadata: string
}

export default function ProviderForm() {
  const navigate = useNavigate()
  const { id } = useParams<{ id: string }>()
  const isEditing = Boolean(id)
  const { tenant } = useCoreTenant()
  const { showSuccess, showError } = useToast()

  const { data: provider, isLoading } = useProvider(id)
  const createM = useCreateProvider()
  const updateM = useUpdateProvider()

  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: { name: '', resource_kind: 'container', driver: 'docker', status: 'active', config: '', metadata: '' },
    mode: 'onTouched',
  })

  useEffect(() => {
    if (isEditing && provider) {
      reset({
        name: provider.name,
        resource_kind: provider.resource_kind,
        driver: provider.driver,
        status: provider.status,
        config: stringifyJson(provider.config),
        metadata: stringifyJson(provider.metadata),
      })
    }
  }, [isEditing, provider, reset])

  const onSubmit = async (values: FormValues) => {
    try {
      const config = parseJsonObject(values.config, 'Config')
      const metadata = parseJsonObject(values.metadata, 'Metadata')
      if (isEditing && id) {
        await updateM.mutateAsync({ id, data: { driver: values.driver, config, status: values.status, metadata } })
        showSuccess('Provider updated')
      } else {
        if (!tenant) {
          showError(new Error('Select a tenant first.'))
          return
        }
        await createM.mutateAsync({
          tenant_uuid: tenant.tenant_uuid,
          name: values.name,
          resource_kind: values.resource_kind,
          driver: values.driver,
          config,
          metadata,
        })
        showSuccess('Provider created')
      }
      navigate('/providers')
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
        backUrl="/providers"
        backLabel="Back to providers"
        title={isEditing ? 'Edit provider' : 'New provider'}
        description={isEditing ? 'Update this provider.' : 'Add a driver that materializes a resource kind.'}
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
              placeholder="docker-local"
              disabled={isEditing}
              error={errors.name?.message}
              {...register('name', { required: 'Name is required' })}
            />
            <FormInputField
              label="Resource kind"
              required
              placeholder="container"
              description="The resource kind this provider materializes."
              disabled={isEditing}
              error={errors.resource_kind?.message}
              {...register('resource_kind', { required: 'Resource kind is required' })}
            />
            <FormInputField
              label="Driver"
              required
              placeholder="docker"
              error={errors.driver?.message}
              {...register('driver', { required: 'Driver is required' })}
            />
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
            <FormTextareaField
              label="Config (JSON)"
              rows={6}
              className="font-mono text-xs"
              placeholder='{ "socket": "/var/run/docker.sock" }'
              error={errors.config?.message}
              {...register('config')}
            />
            <FormTextareaField
              label="Metadata (JSON)"
              rows={4}
              className="font-mono text-xs"
              error={errors.metadata?.message}
              {...register('metadata')}
            />
          </CardContent>
        </Card>
        <div className="flex justify-end gap-3">
          <FormSubmitButton isSubmitting={isSubmitting} submitText={isEditing ? 'Update provider' : 'Create provider'} />
        </div>
      </form>
    </DetailsContainer>
  )
}
