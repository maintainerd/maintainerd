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
import { useService } from '@/hooks/useServices'

export default function ServiceDetails() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: service, isLoading, error } = useService(id)

  if (isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }
  if (error || !service) {
    return (
      <DetailsContainer>
        <FormPageHeader backUrl="/services" backLabel="Back to services" title="Service not found" description="" />
      </DetailsContainer>
    )
  }

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl="/services"
        backLabel="Back to services"
        breadcrumbs={[{ label: 'Services', to: '/services' }, { label: service.name }]}
        title={service.name}
        description={service.kind}
        showSystemBadge={service.is_system}
        headerActions={
          <Button variant="outline" size="sm" onClick={() => navigate(`/services/${service.service_uuid}/edit`)}>
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </Button>
        }
      />
      <div className="mt-6 space-y-6">
        <DetailCard
          title="Overview"
          rows={[
            { label: 'Status', value: <StatusBadge status={service.status} /> },
            { label: 'Kind', value: <Badge variant="outline">{service.kind}</Badge> },
            { label: 'Endpoint', value: service.endpoint ? <span className="font-mono text-xs">{service.endpoint}</span> : '—' },
            { label: 'Version', value: service.version || '—' },
            { label: 'System', value: service.is_system ? 'Yes' : 'No' },
            { label: 'Service UUID', value: <span className="font-mono text-xs">{service.service_uuid}</span> },
            { label: 'Registered', value: safeFormat(service.registered_at, 'MMM d, yyyy p') },
            { label: 'Created', value: safeFormat(service.created_at, 'MMM d, yyyy p') },
          ]}
        />
        <DetailCard title="Metadata" rows={[{ label: 'metadata', value: <JsonBlock value={service.metadata} /> }]} />
      </div>
    </DetailsContainer>
  )
}
