import { useNavigate, useParams } from 'react-router-dom'
import { Pencil } from 'lucide-react'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DetailCard } from '@/components/core/DetailCard'
import { JsonBlock } from '@/components/core/JsonBlock'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { safeFormat } from '@/lib/formatDate'
import { useResource } from '@/hooks/useResources'

export default function ResourceDetails() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: resource, isLoading, error } = useResource(id)

  if (isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }
  if (error || !resource) {
    return (
      <DetailsContainer>
        <FormPageHeader backUrl="/projects" backLabel="Back" title="Resource not found" description="" />
      </DetailsContainer>
    )
  }

  const inSync = resource.observed_generation >= resource.generation

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl={`/projects/${resource.project_uuid}`}
        backLabel="Back to project"
        breadcrumbs={[
          { label: 'Projects', to: '/projects' },
          { label: 'Project', to: `/projects/${resource.project_uuid}` },
          { label: 'Resources' },
          { label: resource.name },
        ]}
        title={resource.name}
        description={resource.kind}
        headerActions={
          <Button variant="outline" size="sm" onClick={() => navigate(`/resources/${resource.resource_uuid}/edit`)}>
            <Pencil className="mr-2 h-4 w-4" />
            Edit spec
          </Button>
        }
      />
      <div className="mt-6 space-y-6">
        <DetailCard
          title="Control loop"
          rows={[
            { label: 'State', value: <StatusBadge status={resource.state} /> },
            {
              label: 'Sync',
              value: (
                <StatusBadge
                  status={inSync ? 'active' : 'pending'}
                  label={inSync ? 'In sync' : 'Reconciling'}
                />
              ),
            },
            { label: 'Desired generation', value: resource.generation },
            { label: 'Observed generation', value: resource.observed_generation },
            { label: 'Kind', value: <Badge variant="outline">{resource.kind}</Badge> },
            { label: 'Provider', value: resource.provider_uuid ? <span className="font-mono text-xs">{resource.provider_uuid}</span> : '—' },
            { label: 'Resource UUID', value: <span className="font-mono text-xs">{resource.resource_uuid}</span> },
            { label: 'Updated', value: safeFormat(resource.updated_at, 'MMM d, yyyy p') },
          ]}
        />
        <DetailCard title="Spec (desired)" rows={[{ label: 'spec', value: <JsonBlock value={resource.spec} /> }]} />
        <DetailCard title="Status (observed)" rows={[{ label: 'status', value: <JsonBlock value={resource.status} empty="No status reported yet" /> }]} />
        <DetailCard title="Metadata" rows={[{ label: 'metadata', value: <JsonBlock value={resource.metadata} /> }]} />
      </div>
    </DetailsContainer>
  )
}
