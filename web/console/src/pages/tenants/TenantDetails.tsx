import { useNavigate, useParams } from 'react-router-dom'
import { Pencil, Boxes } from 'lucide-react'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Button } from '@/components/ui/button'
import { DetailCard } from '@/components/core/DetailCard'
import { JsonBlock } from '@/components/core/JsonBlock'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { safeFormat } from '@/lib/formatDate'
import { useTenant } from '@/hooks/useTenants'
import { useCoreTenant } from '@/context/CoreTenantContext'

export default function TenantDetails() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: tenant, isLoading, error } = useTenant(id)
  const { tenantUuid, setTenantUuid } = useCoreTenant()

  if (isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }
  if (error || !tenant) {
    return (
      <DetailsContainer>
        <FormPageHeader backUrl="/tenants" backLabel="Back to tenants" title="Tenant not found" description="" />
      </DetailsContainer>
    )
  }

  const isActive = tenantUuid === tenant.tenant_uuid

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl="/tenants"
        backLabel="Back to tenants"
        title={tenant.display_name || tenant.name}
        description={tenant.name}
        showSystemBadge={tenant.is_system}
        headerActions={
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={isActive}
              onClick={() => {
                setTenantUuid(tenant.tenant_uuid)
                navigate('/projects')
              }}
            >
              <Boxes className="mr-2 h-4 w-4" />
              {isActive ? 'Active tenant' : 'Set active & view projects'}
            </Button>
            <Button variant="outline" size="sm" onClick={() => navigate(`/tenants/${tenant.tenant_uuid}/edit`)}>
              <Pencil className="mr-2 h-4 w-4" />
              Edit
            </Button>
          </div>
        }
      />
      <div className="mt-6 space-y-6">
        <DetailCard
          title="Overview"
          rows={[
            { label: 'Status', value: <StatusBadge status={tenant.status} /> },
            { label: 'Type', value: tenant.is_system ? <Badge variant="secondary">System</Badge> : <Badge variant="outline">Tenant</Badge> },
            { label: 'Tenant UUID', value: <span className="font-mono text-xs">{tenant.tenant_uuid}</span> },
            { label: 'Auth tenant UUID', value: tenant.auth_tenant_uuid ? <span className="font-mono text-xs">{tenant.auth_tenant_uuid}</span> : '—' },
            { label: 'Created', value: safeFormat(tenant.created_at, 'MMM d, yyyy p') },
            { label: 'Updated', value: safeFormat(tenant.updated_at, 'MMM d, yyyy p') },
          ]}
        />
        <DetailCard title="Metadata" rows={[{ label: 'metadata', value: <JsonBlock value={tenant.metadata} /> }]} />
      </div>
    </DetailsContainer>
  )
}
