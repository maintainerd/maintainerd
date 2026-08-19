import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { Trash2 } from 'lucide-react'
import { ResourceListing } from '@/components/data-table/ResourceListing'
import { DataTableColumnHeader } from '@/components/data-table/DataTableColumnHeader'
import { RowActions, type RowActionItem } from '@/components/data-table/RowActions'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { ResourceScopeProvider } from '@/context/ResourceScopeContext'
import { useResources, useDeleteResource } from '@/hooks/useResources'
import type { Resource, ResourceListParams } from '@/services/api/resources/types'
import { useToast } from '@/hooks/useToast'

const DEFAULT_SORT: SortingState = [{ id: 'created_at', desc: true }]

/** In sync when the agent has observed the latest desired generation. */
function SyncBadge({ resource }: { resource: Resource }) {
  const inSync = resource.observed_generation >= resource.generation
  return (
    <StatusBadge
      status={inSync ? 'active' : 'pending'}
      label={inSync ? 'In sync' : `Reconciling (${resource.observed_generation}/${resource.generation})`}
    />
  )
}

function ResourceRowActions({ resource }: { resource: Resource }) {
  const del = useDeleteResource()
  const { showSuccess, showError } = useToast()
  const items: RowActionItem[] = [
    {
      key: 'delete',
      label: 'Delete',
      icon: Trash2,
      destructive: true,
      confirm: {
        title: 'Delete resource',
        description: `This removes "${resource.name}". The agent will tear down the running workload.`,
        destructive: true,
        itemName: resource.name,
      },
      onSelect: async () => {
        try {
          await del.mutateAsync(resource.resource_uuid)
          showSuccess('Resource deleted')
        } catch (e) {
          showError(e)
        }
      },
    },
  ]
  return <RowActions items={items} />
}

/** The resource table for one project. Polls live so the control loop shows. */
export function ResourceListingSection({ projectUuid }: { projectUuid: string }) {
  const navigate = useNavigate()

  const columns = useMemo<ColumnDef<Resource>[]>(
    () => [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />,
        cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span>,
      },
      {
        id: 'kind',
        accessorKey: 'kind',
        header: 'Kind',
        cell: ({ row }) => <Badge variant="outline">{row.original.kind}</Badge>,
      },
      {
        id: 'state',
        accessorKey: 'state',
        header: 'State',
        cell: ({ row }) => <StatusBadge status={row.original.state} />,
      },
      { id: 'sync', header: 'Sync', cell: ({ row }) => <SyncBadge resource={row.original} /> },
      { id: 'actions', cell: ({ row }) => <ResourceRowActions resource={row.original} /> },
    ],
    [],
  )

  return (
    <ResourceScopeProvider projectUuid={projectUuid}>
      <ResourceListing<Resource, ResourceListParams>
        columns={columns}
        defaultSort={DEFAULT_SORT}
        searchFields={['name']}
        searchPlaceholder="Search resources…"
        useData={useResources}
        onRowClick={(r) => navigate(`/resources/${r.resource_uuid}`)}
        onCreate={() => navigate(`/projects/${projectUuid}/resources/create`)}
        createLabel="New Resource"
        emptyTitle="No resources yet"
        emptyDescription="Declare a resource and the control loop will reconcile it."
        tableInCard
      />
    </ResourceScopeProvider>
  )
}
