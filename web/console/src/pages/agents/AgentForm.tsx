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
import { useAgent, useCreateAgent, useUpdateAgent } from '@/hooks/useAgents'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

interface FormValues {
  name: string
  endpoint: string
  version: string
  status: string
  capabilities: string
  metadata: string
}

function splitCapabilities(text: string): string[] {
  return text
    .split(',')
    .map((c) => c.trim())
    .filter(Boolean)
}

export default function AgentForm() {
  const navigate = useNavigate()
  const { id } = useParams<{ id: string }>()
  const isEditing = Boolean(id)
  const { tenant } = useCoreTenant()
  const { showSuccess, showError } = useToast()

  const { data: agent, isLoading } = useAgent(id)
  const createM = useCreateAgent()
  const updateM = useUpdateAgent()

  const {
    register,
    control,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    defaultValues: { name: '', endpoint: '', version: '', status: 'active', capabilities: '', metadata: '' },
    mode: 'onTouched',
  })

  useEffect(() => {
    if (isEditing && agent) {
      reset({
        name: agent.name,
        endpoint: agent.endpoint ?? '',
        version: agent.version ?? '',
        status: agent.status,
        capabilities: (agent.capabilities ?? []).join(', '),
        metadata: stringifyJson(agent.metadata),
      })
    }
  }, [isEditing, agent, reset])

  const onSubmit = async (values: FormValues) => {
    try {
      if (isEditing && id) {
        await updateM.mutateAsync({
          id,
          data: {
            status: values.status,
            endpoint: values.endpoint || undefined,
            version: values.version || undefined,
            capabilities: splitCapabilities(values.capabilities),
          },
        })
        showSuccess('Agent updated')
      } else {
        if (!tenant) {
          showError(new Error('Select a tenant first.'))
          return
        }
        const metadata = parseJsonObject(values.metadata, 'Metadata')
        await createM.mutateAsync({
          tenant_uuid: tenant.tenant_uuid,
          name: values.name,
          endpoint: values.endpoint || undefined,
          version: values.version || undefined,
          capabilities: splitCapabilities(values.capabilities),
          metadata,
        })
        showSuccess('Agent created')
      }
      navigate('/agents')
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
        backUrl="/agents"
        backLabel="Back to agents"
        title={isEditing ? 'Edit agent' : 'New agent'}
        description={isEditing ? 'Update this agent registration.' : 'Register an executor for this tenant.'}
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
              placeholder="agent-1"
              disabled={isEditing}
              error={errors.name?.message}
              {...register('name', { required: 'Name is required' })}
            />
            <FormInputField label="Endpoint" placeholder="agent-1:9091" error={errors.endpoint?.message} {...register('endpoint')} />
            <FormInputField label="Version" placeholder="v1" error={errors.version?.message} {...register('version')} />
            <FormInputField
              label="Capabilities"
              placeholder="container, exec"
              description="Comma-separated list of capabilities the agent advertises."
              error={errors.capabilities?.message}
              {...register('capabilities')}
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
          <FormSubmitButton isSubmitting={isSubmitting} submitText={isEditing ? 'Update agent' : 'Create agent'} />
        </div>
      </form>
    </DetailsContainer>
  )
}
