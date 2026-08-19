import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { Building2, Pencil, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/layout/PageHeader'
import { ResourceListing } from '@/components/data-table/ResourceListing'
import { DataTableColumnHeader } from '@/components/data-table/DataTableColumnHeader'
import { RowActions, type RowActionItem } from '@/components/data-table/RowActions'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { safeFormat } from '@/lib/formatDate'
import { useTenants, useDeleteTenant } from '@/hooks/useTenants'
import type { Tenant, TenantListParams } from '@/services/api/tenants/types'
import { useToast } from '@/hooks/useToast'

const DEFAULT_SORT: SortingState = [{ id: 'created_at', desc: true }]

function TenantRowActions({ tenant }: { tenant: Tenant }) {
  const navigate = useNavigate()
  const del = useDeleteTenant()
  const { showSuccess, showError } = useToast()

  const items: RowActionItem[] = [
    { key: 'edit', label: 'Edit', icon: Pencil, onSelect: () => navigate(`/tenants/${tenant.tenant_uuid}/edit`) },
  ]
  // The system tenant is delete-protected by the API — don't offer it.
  if (!tenant.is_system) {
    items.push({
      key: 'delete',
      label: 'Delete',
      icon: Trash2,
      destructive: true,
      confirm: {
        title: 'Delete tenant',
        description: `This permanently removes "${tenant.name}" and everything scoped to it.`,
        destructive: true,
        itemName: tenant.name,
      },
      onSelect: async () => {
        try {
          await del.mutateAsync(tenant.tenant_uuid)
          showSuccess('Tenant deleted')
        } catch (e) {
          showError(e)
        }
      },
    })
  }
  return <RowActions items={items} />
}

export default function TenantsPage() {
  const navigate = useNavigate()

  const columns = useMemo<ColumnDef<Tenant>[]>(
    () => [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />,
        cell: ({ row }) => {
          const t = row.original
          return (
            <div className="min-w-0">
              <div className="font-medium text-foreground">{t.display_name || t.name}</div>
              <div className="truncate text-xs text-muted-foreground">{t.name}</div>
            </div>
          )
        },
      },
      {
        id: 'type',
        header: 'Type',
        cell: ({ row }) =>
          row.original.is_system ? <Badge variant="secondary">System</Badge> : <Badge variant="outline">Tenant</Badge>,
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
        cell: ({ row }) => (
          <span className="text-sm text-muted-foreground">{safeFormat(row.original.created_at, 'MMM d, yyyy')}</span>
        ),
      },
      {
        id: 'actions',
        cell: ({ row }) => <TenantRowActions tenant={row.original} />,
      },
    ],
    [],
  )

  return (
    <div className="space-y-6">
      <PageHeader title="Tenants" description="Isolation boundaries governing projects, services, providers and agents." icon={Building2} />
      <ResourceListing<Tenant, TenantListParams>
        columns={columns}
        defaultSort={DEFAULT_SORT}
        searchFields={['name']}
        searchPlaceholder="Search tenants…"
        useData={useTenants}
        filterGroups={[{ key: 'status', label: 'Status', options: ['active', 'inactive', 'suspended'] }]}
        onRowClick={(t) => navigate(`/tenants/${t.tenant_uuid}`)}
        onCreate={() => navigate('/tenants/create')}
        createLabel="New Tenant"
        emptyTitle="No tenants yet"
        emptyDescription="Create your first tenant to start declaring projects and resources."
        tableInCard
      />
    </div>
  )
}
