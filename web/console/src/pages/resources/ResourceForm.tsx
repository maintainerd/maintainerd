import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Controller, useForm } from 'react-hook-form'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { FormInputField, FormSelectField, FormTextareaField, type SelectOption } from '@/components/form'
import FormSubmitButton from '@/components/form/FormSubmitButton'
import { parseJsonObject, stringifyJson } from '@/lib/json'
import { useResource, useCreateResource, useUpdateResourceSpec } from '@/hooks/useResources'
import { useProviders } from '@/hooks/useProviders'
import { useToast } from '@/hooks/useToast'

interface FormValues {
  name: string
  kind: string
  provider_uuid: string
  spec: string
  metadata: string
}

const CONTAINER_SPEC_EXAMPLE = `{
  "image": "nginx:alpine",
  "name": "my-web"
}`

export default function ResourceForm() {
  const navigate = useNavigate()
  // On create the route is /projects/:projectId/resources/create.
  // On edit the route is /resources/:id/edit.
  const { projectId, id } = useParams<{ projectId?: string; id?: string }>()
  const isEditing = Boolean(id)
  const { showSuccess, showError } = useToast()

  const { data: resource, isLoading } = useResource(id)
  const createM = useCreateResource()
  const updateM = useUpdateResourceSpec()
  const { data: providersData } = useProviders({ limit: 100 })

  const providerOptions: SelectOption[] = [
    { value: '', label: 'None' },
    ...(providersData?.rows ?? []).map((p) => ({ value: p.provider_uuid, label: `${p.name} (${p.resource_kind})` })),
  ]

  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: { name: '', kind: 'container', provider_uuid: '', spec: CONTAINER_SPEC_EXAMPLE, metadata: '' },
    mode: 'onTouched',
  })

  useEffect(() => {
    if (isEditing && resource) {
      reset({
        name: resource.name,
        kind: resource.kind,
        provider_uuid: resource.provider_uuid ?? '',
        spec: stringifyJson(resource.spec),
        metadata: stringifyJson(resource.metadata),
      })
    }
  }, [isEditing, resource, reset])

  const onSubmit = async (values: FormValues) => {
    try {
      const spec = parseJsonObject(values.spec, 'Spec')
      const metadata = parseJsonObject(values.metadata, 'Metadata')
      if (isEditing && id) {
        await updateM.mutateAsync({ id, data: { spec, metadata } })
        showSuccess('Resource updated — reconciler re-armed')
        navigate(`/resources/${id}`)
      } else {
        if (!projectId) {
          showError(new Error('Missing project.'))
          return
        }
        const created = await createM.mutateAsync({
          project_uuid: projectId,
          provider_uuid: values.provider_uuid || null,
          kind: values.kind,
          name: values.name,
          spec,
          metadata,
        })
        showSuccess('Resource created')
        navigate(`/resources/${created.resource_uuid}`)
      }
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

  const backUrl = isEditing && resource ? `/resources/${resource.resource_uuid}` : projectId ? `/projects/${projectId}` : '/projects'

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl={backUrl}
        backLabel="Back"
        title={isEditing ? 'Edit resource' : 'New resource'}
        description={isEditing ? 'Editing the spec bumps the generation and re-runs the control loop.' : 'Declare a desired-state resource for the control loop to reconcile.'}
      />
      <form onSubmit={handleSubmit(onSubmit)} className="mt-6 space-y-6">
        <Card>
          <CardHeader>
            <CardTitle className="text-base font-semibold">Definition</CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            <FormInputField
              label="Name"
              required
              placeholder="my-web"
              disabled={isEditing}
              error={errors.name?.message}
              {...register('name', { required: 'Name is required' })}
            />
            <FormInputField
              label="Kind"
              required
              placeholder="container"
              description="Resource type discriminator (e.g. container). The agent interprets the spec by kind."
              disabled={isEditing}
              error={errors.kind?.message}
              {...register('kind', { required: 'Kind is required' })}
            />
            {!isEditing && (
              <Controller
                name="provider_uuid"
                control={control}
                render={({ field }) => (
                  <FormSelectField
                    label="Provider"
                    options={providerOptions}
                    value={field.value}
                    onValueChange={field.onChange}
                    description="Optional — the driver that materializes this resource."
                    error={errors.provider_uuid?.message}
                  />
                )}
              />
            )}
            <FormTextareaField
              label="Spec (JSON)"
              required
              rows={8}
              className="font-mono text-xs"
              description="Desired state. For a container: image, name, env, etc."
              error={errors.spec?.message}
              {...register('spec', { required: 'Spec is required' })}
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
          <FormSubmitButton isSubmitting={isSubmitting} submitText={isEditing ? 'Update resource' : 'Create resource'} />
        </div>
      </form>
    </DetailsContainer>
  )
}
