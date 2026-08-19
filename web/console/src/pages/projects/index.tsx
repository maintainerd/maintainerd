import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ColumnDef, SortingState } from '@tanstack/react-table'
import { FolderKanban, Pencil, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/layout/PageHeader'
import { ResourceListing } from '@/components/data-table/ResourceListing'
import { DataTableColumnHeader } from '@/components/data-table/DataTableColumnHeader'
import { RowActions, type RowActionItem } from '@/components/data-table/RowActions'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { safeFormat } from '@/lib/formatDate'
import { useProjects, useDeleteProject } from '@/hooks/useProjects'
import type { Project, ProjectListParams } from '@/services/api/projects/types'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useToast } from '@/hooks/useToast'

const DEFAULT_SORT: SortingState = [{ id: 'created_at', desc: true }]

function ProjectRowActions({ project }: { project: Project }) {
  const navigate = useNavigate()
  const del = useDeleteProject()
  const { showSuccess, showError } = useToast()
  const items: RowActionItem[] = [
    { key: 'edit', label: 'Edit', icon: Pencil, onSelect: () => navigate(`/projects/${project.project_uuid}/edit`) },
    {
      key: 'delete',
      label: 'Delete',
      icon: Trash2,
      destructive: true,
      confirm: {
        title: 'Delete project',
        description: `This permanently removes "${project.name}" and its resources.`,
        destructive: true,
        itemName: project.name,
      },
      onSelect: async () => {
        try {
          await del.mutateAsync(project.project_uuid)
          showSuccess('Project deleted')
        } catch (e) {
          showError(e)
        }
      },
    },
  ]
  return <RowActions items={items} />
}

export default function ProjectsPage() {
  const navigate = useNavigate()
  const { tenant } = useCoreTenant()

  const columns = useMemo<ColumnDef<Project>[]>(
    () => [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <DataTableColumnHeader column={column} title="Name" />,
        cell: ({ row }) => {
          const p = row.original
          return (
            <div className="min-w-0">
              <div className="font-medium text-foreground">{p.display_name || p.name}</div>
              <div className="truncate text-xs text-muted-foreground">{p.name}</div>
            </div>
          )
        },
      },
      {
        id: 'description',
        header: 'Description',
        cell: ({ row }) => (
          <span className="line-clamp-1 text-sm text-muted-foreground">{row.original.description || '—'}</span>
        ),
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
      { id: 'actions', cell: ({ row }) => <ProjectRowActions project={row.original} /> },
    ],
    [],
  )

  return (
    <div className="space-y-6">
      <PageHeader
        title="Projects"
        description={tenant ? `Workloads in ${tenant.display_name || tenant.name}.` : 'Select a tenant to view its projects.'}
        icon={FolderKanban}
      />
      <ResourceListing<Project, ProjectListParams>
        columns={columns}
        defaultSort={DEFAULT_SORT}
        searchFields={['name']}
        searchPlaceholder="Search projects…"
        useData={useProjects}
        filterGroups={[{ key: 'status', label: 'Status', options: ['active', 'inactive', 'pending', 'maintenance', 'suspended'] }]}
        onRowClick={(p) => navigate(`/projects/${p.project_uuid}`)}
        onCreate={() => navigate('/projects/create')}
        createLabel="New Project"
        emptyTitle="No projects yet"
        emptyDescription="Create a project to group and declare resources."
        tableInCard
      />
    </div>
  )
}
