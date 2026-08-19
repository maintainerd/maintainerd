import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { Server, Pencil, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/layout/PageHeader'
import { ResourceListing } from '@/components/data-table/ResourceListing'
import { DataTableColumnHeader } from '@/components/data-table/DataTableColumnHeader'
import { RowActions, type RowActionItem } from '@/components/data-table/RowActions'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { safeFormat } from '@/lib/formatDate'
import { useServices, useDeleteService } from '@/hooks/useServices'
import type { ServiceRegistration, ServiceListParams } from '@/services/api/services/types'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

const DEFAULT_SORT: SortingState = [{ id: 'created_at', desc: true }]

function ServiceRowActions({ service }: { service: ServiceRegistration }) {
  const navigate = useNavigate()
  const del = useDeleteService()
  const { showSuccess, showError } = useToast()
  const items: RowActionItem[] = [
    { key: 'edit', label: 'Edit', icon: Pencil, onSelect: () => navigate(`/services/${service.service_uuid}/edit`) },
  ]
  if (!service.is_system) {
    items.push({
      key: 'delete',
      label: 'Delete',
      icon: Trash2,
      destructive: true,
      confirm: {
        title: 'Delete service',
        description: `This removes the "${service.name}" service registration.`,
        destructive: true,
        itemName: service.name,
      },
      onSelect: async () => {
        try {
          await del.mutateAsync(service.service_uuid)
          showSuccess('Service deleted')
        } catch (e) {
          showError(e)
        }
      },
    })
  }
  return <RowActions items={items} />
}

export default function ServicesPage() {
  const navigate = useNavigate()
  const { tenant } = useCoreTenant()

  const columns = useMemo<ColumnDef<ServiceRegistration>[]>(
    () => [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />,
        cell: ({ row }) => {
          const s = row.original
          return (
            <div className="flex items-center gap-2">
              <span className="font-medium text-foreground">{s.name}</span>
              {s.is_system && <Badge variant="secondary">System</Badge>}
            </div>
          )
        },
      },
      { id: 'kind', accessorKey: 'kind', header: 'Kind', cell: ({ row }) => <Badge variant="outline">{row.original.kind}</Badge> },
      {
        id: 'endpoint',
        header: 'Endpoint',
        cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.endpoint || '—'}</span>,
      },
      {
        id: 'status',
        accessorKey: 'status',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Status" />,
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        id: 'created_at',
        accessorKey: 'created_at',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Created" />,
        cell: ({ row }) => <span className="text-sm text-muted-foreground">{safeFormat(row.original.created_at, 'MMM d, yyyy')}</span>,
      },
      { id: 'actions', cell: ({ row }) => <ServiceRowActions service={row.original} /> },
    ],
    [],
  )

  return (
    <div className="space-y-6">
      <PageHeader
        title="Services"
        description={tenant ? `Registered services in ${tenant.display_name || tenant.name}.` : 'Select a tenant to view its services.'}
        icon={Server}
      />
      <ResourceListing<ServiceRegistration, ServiceListParams>
        columns={columns}
        defaultSort={DEFAULT_SORT}
        searchFields={['name']}
        searchPlaceholder="Search services…"
        useData={useServices}
        filterGroups={[{ key: 'status', label: 'Status', options: ['active', 'inactive', 'pending', 'maintenance', 'suspended'] }]}
        onRowClick={(s) => navigate(`/services/${s.service_uuid}`)}
        onCreate={() => navigate('/services/create')}
        createLabel="Register Service"
        emptyTitle="No services yet"
        emptyDescription="Register a service so the tenant can govern it."
        tableInCard
      />
    </div>
  )
}
