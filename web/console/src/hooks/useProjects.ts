import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listProjects, getProject, createProject, updateProject, deleteProject } from '@/services/api/projects'
import type { ProjectListParams, CreateProjectRequest, UpdateProjectRequest } from '@/services/api/projects/types'
import { useCoreTenant } from '@/context/CoreTenantContext'

export const projectKeys = {
  all: ['projects'] as const,
  lists: () => [...projectKeys.all, 'list'] as const,
  list: (p?: ProjectListParams) => [...projectKeys.lists(), p] as const,
  details: () => [...projectKeys.all, 'detail'] as const,
  detail: (id: string) => [...projectKeys.details(), id] as const,
}

/** Tenant-scoped list hook shaped for the data-table engine. */
export function useProjects(params?: ProjectListParams) {
  const { tenantUuid } = useCoreTenant()
  return useQuery({
    queryKey: projectKeys.list({ ...params, tenant: tenantUuid }),
    queryFn: async () => {
      if (!tenantUuid) return { rows: [], total: 0 }
      const { items, total } = await listProjects({ ...params, tenant: tenantUuid })
      return { rows: items, total }
    },
    enabled: !!tenantUuid,
    placeholderData: keepPreviousData,
  })
}

export function useProject(id?: string) {
  return useQuery({ queryKey: projectKeys.detail(id ?? ''), queryFn: () => getProject(id!), enabled: !!id })
}

export function useCreateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: CreateProjectRequest) => createProject(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: projectKeys.all }),
  })
}

export function useUpdateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateProjectRequest }) => updateProject(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: projectKeys.all }),
  })
}

export function useDeleteProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteProject(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: projectKeys.all }),
  })
}
