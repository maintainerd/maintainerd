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
import { useProvider } from '@/hooks/useProviders'

export default function ProviderDetails() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: provider, isLoading, error } = useProvider(id)

  if (isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }
  if (error || !provider) {
    return (
      <DetailsContainer>
        <FormPageHeader backUrl="/providers" backLabel="Back to providers" title="Provider not found" description="" />
      </DetailsContainer>
    )
  }

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl="/providers"
        backLabel="Back to providers"
        title={provider.name}
        description={`${provider.driver} · ${provider.resource_kind}`}
        headerActions={
          <Button variant="outline" size="sm" onClick={() => navigate(`/providers/${provider.provider_uuid}/edit`)}>
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </Button>
        }
      />
      <div className="mt-6 space-y-6">
        <DetailCard
          title="Overview"
          rows={[
            { label: 'Status', value: <StatusBadge status={provider.status} /> },
            { label: 'Driver', value: provider.driver },
            { label: 'Resource kind', value: <Badge variant="outline">{provider.resource_kind}</Badge> },
            { label: 'Provider UUID', value: <span className="font-mono text-xs">{provider.provider_uuid}</span> },
            { label: 'Created', value: safeFormat(provider.created_at, 'MMM d, yyyy p') },
            { label: 'Updated', value: safeFormat(provider.updated_at, 'MMM d, yyyy p') },
          ]}
        />
        <DetailCard title="Config" rows={[{ label: 'config', value: <JsonBlock value={provider.config} /> }]} />
        <DetailCard title="Metadata" rows={[{ label: 'metadata', value: <JsonBlock value={provider.metadata} /> }]} />
      </div>
    </DetailsContainer>
  )
}
