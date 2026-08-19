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
import { useProject, useCreateProject, useUpdateProject } from '@/hooks/useProjects'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

interface FormValues {
  name: string
  display_name: string
  description: string
  status: string
  metadata: string
}

export default function ProjectForm() {
  const navigate = useNavigate()
  const { id } = useParams<{ id: string }>()
  const isEditing = Boolean(id)
  const { tenant } = useCoreTenant()
  const { showSuccess, showError } = useToast()

  const { data: project, isLoading } = useProject(id)
  const createM = useCreateProject()
  const updateM = useUpdateProject()

  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: { name: '', display_name: '', description: '', status: 'active', metadata: '' },
    mode: 'onTouched',
  })

  useEffect(() => {
    if (isEditing && project) {
      reset({
        name: project.name,
        display_name: project.display_name ?? '',
        description: project.description ?? '',
        status: project.status,
        metadata: stringifyJson(project.metadata),
      })
    }
  }, [isEditing, project, reset])

  const onSubmit = async (values: FormValues) => {
    try {
      const metadata = parseJsonObject(values.metadata, 'Metadata')
      if (isEditing && id) {
        await updateM.mutateAsync({
          id,
          data: {
            display_name: values.display_name || undefined,
            description: values.description || undefined,
            status: values.status,
            metadata,
          },
        })
        showSuccess('Project updated')
      } else {
        if (!tenant) {
          showError(new Error('Select a tenant first.'))
          return
        }
        await createM.mutateAsync({
          tenant_uuid: tenant.tenant_uuid,
          name: values.name,
          display_name: values.display_name || undefined,
          description: values.description || undefined,
          metadata,
        })
        showSuccess('Project created')
      }
      navigate('/projects')
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
        backUrl="/projects"
        backLabel="Back to projects"
        title={isEditing ? 'Edit project' : 'New project'}
        description={
          isEditing
            ? 'Update this project.'
            : tenant
              ? `Create a project in ${tenant.display_name || tenant.name}.`
              : 'Select a tenant first.'
        }
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
              placeholder="default"
              description="Stable machine name. Cannot be changed after creation."
              disabled={isEditing}
              error={errors.name?.message}
              {...register('name', { required: 'Name is required' })}
            />
            <FormInputField
              label="Display name"
              placeholder="Default project"
              error={errors.display_name?.message}
              {...register('display_name')}
            />
            <FormTextareaField
              label="Description"
              rows={3}
              placeholder="What this project holds…"
              error={errors.description?.message}
              {...register('description')}
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
              label="Metadata (JSON)"
              placeholder='{ "env": "production" }'
              rows={5}
              className="font-mono text-xs"
              error={errors.metadata?.message}
              {...register('metadata')}
            />
          </CardContent>
        </Card>
        <div className="flex justify-end gap-3">
          <FormSubmitButton isSubmitting={isSubmitting} submitText={isEditing ? 'Update project' : 'Create project'} />
        </div>
      </form>
    </DetailsContainer>
  )
}
