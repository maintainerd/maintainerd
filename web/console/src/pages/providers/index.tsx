import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { Plug, Pencil, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/layout/PageHeader'
import { ResourceListing } from '@/components/data-table/ResourceListing'
import { DataTableColumnHeader } from '@/components/data-table/DataTableColumnHeader'
import { RowActions, type RowActionItem } from '@/components/data-table/RowActions'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { safeFormat } from '@/lib/formatDate'
import { useProviders, useDeleteProvider } from '@/hooks/useProviders'
import type { Provider, ProviderListParams } from '@/services/api/providers/types'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

const DEFAULT_SORT: SortingState = [{ id: 'created_at', desc: true }]

function ProviderRowActions({ provider }: { provider: Provider }) {
  const navigate = useNavigate()
  const del = useDeleteProvider()
  const { showSuccess, showError } = useToast()
  const items: RowActionItem[] = [
    { key: 'edit', label: 'Edit', icon: Pencil, onSelect: () => navigate(`/providers/${provider.provider_uuid}/edit`) },
    {
      key: 'delete',
      label: 'Delete',
      icon: Trash2,
      destructive: true,
      confirm: {
        title: 'Delete provider',
        description: `This removes the "${provider.name}" provider.`,
        destructive: true,
        itemName: provider.name,
      },
      onSelect: async () => {
        try {
          await del.mutateAsync(provider.provider_uuid)
          showSuccess('Provider deleted')
        } catch (e) {
          showError(e)
        }
      },
    },
  ]
  return <RowActions items={items} />
}

export default function ProvidersPage() {
  const navigate = useNavigate()
  const { tenant } = useCoreTenant()

  const columns = useMemo<ColumnDef<Provider>[]>(
    () => [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />,
        cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span>,
      },
      {
        id: 'resource_kind',
        accessorKey: 'resource_kind',
        header: 'Resource kind',
        cell: ({ row }) => <Badge variant="outline">{row.original.resource_kind}</Badge>,
      },
      { id: 'driver', accessorKey: 'driver', header: 'Driver', cell: ({ row }) => <span className="text-sm">{row.original.driver}</span> },
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
      { id: 'actions', cell: ({ row }) => <ProviderRowActions provider={row.original} /> },
    ],
    [],
  )

  return (
    <div className="space-y-6">
      <PageHeader
        title="Providers"
        description={tenant ? `Resource drivers in ${tenant.display_name || tenant.name}.` : 'Select a tenant to view its providers.'}
        icon={Plug}
      />
      <ResourceListing<Provider, ProviderListParams>
        columns={columns}
        defaultSort={DEFAULT_SORT}
        searchFields={['name']}
        searchPlaceholder="Search providers…"
        useData={useProviders}
        filterGroups={[{ key: 'status', label: 'Status', options: ['active', 'inactive', 'pending', 'maintenance', 'suspended'] }]}
        onRowClick={(p) => navigate(`/providers/${p.provider_uuid}`)}
        onCreate={() => navigate('/providers/create')}
        createLabel="New Provider"
        emptyTitle="No providers yet"
        emptyDescription="Add a provider to materialize resources of a given kind."
        tableInCard
      />
    </div>
  )
}
