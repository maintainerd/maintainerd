import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { Cpu, Pencil, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/layout/PageHeader'
import { ResourceListing } from '@/components/data-table/ResourceListing'
import { DataTableColumnHeader } from '@/components/data-table/DataTableColumnHeader'
import { RowActions, type RowActionItem } from '@/components/data-table/RowActions'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { safeFormat } from '@/lib/formatDate'
import { useAgents, useDeleteAgent } from '@/hooks/useAgents'
import type { Agent, AgentListParams } from '@/services/api/agents/types'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

const DEFAULT_SORT: SortingState = [{ id: 'created_at', desc: true }]

function AgentRowActions({ agent }: { agent: Agent }) {
  const navigate = useNavigate()
  const del = useDeleteAgent()
  const { showSuccess, showError } = useToast()
  const items: RowActionItem[] = [
    { key: 'edit', label: 'Edit', icon: Pencil, onSelect: () => navigate(`/agents/${agent.agent_uuid}/edit`) },
    {
      key: 'delete',
      label: 'Delete',
      icon: Trash2,
      destructive: true,
      confirm: {
        title: 'Delete agent',
        description: `This removes the "${agent.name}" agent registration.`,
        destructive: true,
        itemName: agent.name,
      },
      onSelect: async () => {
        try {
          await del.mutateAsync(agent.agent_uuid)
          showSuccess('Agent deleted')
        } catch (e) {
          showError(e)
        }
      },
    },
  ]
  return <RowActions items={items} />
}

export default function AgentsPage() {
  const navigate = useNavigate()
  const { tenant } = useCoreTenant()

  const columns = useMemo<ColumnDef<Agent>[]>(
    () => [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />,
        cell: ({ row }) => <span className="font-medium text-foreground">{row.original.name}</span>,
      },
      {
        id: 'status',
        accessorKey: 'status',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Status" />,
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        id: 'version',
        header: 'Version',
        cell: ({ row }) => <span className="text-sm text-muted-foreground">{row.original.version || '—'}</span>,
      },
      {
        id: 'last_seen_at',
        header: 'Last seen',
        cell: ({ row }) => (
          <span className="text-sm text-muted-foreground">{safeFormat(row.original.last_seen_at, 'MMM d, yyyy p')}</span>
        ),
      },
      { id: 'actions', cell: ({ row }) => <AgentRowActions agent={row.original} /> },
    ],
    [],
  )

  return (
    <div className="space-y-6">
      <PageHeader
        title="Agents"
        description={tenant ? `Executors in ${tenant.display_name || tenant.name}.` : 'Select a tenant to view its agents.'}
        icon={Cpu}
      />
      <ResourceListing<Agent, AgentListParams>
        columns={columns}
        defaultSort={DEFAULT_SORT}
        searchFields={['name']}
        searchPlaceholder="Search agents…"
        useData={useAgents}
        filterGroups={[{ key: 'status', label: 'Status', options: ['active', 'inactive', 'pending', 'maintenance', 'suspended'] }]}
        onRowClick={(a) => navigate(`/agents/${a.agent_uuid}`)}
        onCreate={() => navigate('/agents/create')}
        createLabel="New Agent"
        emptyTitle="No agents yet"
        emptyDescription="Agents pull work from core and run it via a runtime."
        tableInCard
      />
    </div>
  )
}
