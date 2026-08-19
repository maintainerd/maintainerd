import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listResources,
  getResource,
  createResource,
  updateResourceSpec,
  deleteResource,
} from '@/services/api/resources'
import type {
  ResourceListParams,
  CreateResourceRequest,
  UpdateResourceSpecRequest,
} from '@/services/api/resources/types'
import { useResourceScope } from '@/context/ResourceScopeContext'

export const resourceKeys = {
  all: ['resources'] as const,
  lists: () => [...resourceKeys.all, 'list'] as const,
  list: (p?: ResourceListParams) => [...resourceKeys.lists(), p] as const,
  details: () => [...resourceKeys.all, 'detail'] as const,
  detail: (id: string) => [...resourceKeys.details(), id] as const,
}

/**
 * Project-scoped list hook shaped for the data-table engine. Polls every 5s so
 * the observed state (the control loop's progress) stays live.
 */
export function useResources(params?: ResourceListParams) {
  const { projectUuid } = useResourceScope()
  return useQuery({
    queryKey: resourceKeys.list({ ...params, project: projectUuid }),
    queryFn: async () => {
      const { items, total } = await listResources({ ...params, project: projectUuid })
      return { rows: items, total }
    },
    enabled: !!projectUuid,
    placeholderData: keepPreviousData,
    refetchInterval: 5000,
  })
}

export function useResource(id?: string) {
  return useQuery({
    queryKey: resourceKeys.detail(id ?? ''),
    queryFn: () => getResource(id!),
    enabled: !!id,
    refetchInterval: 5000,
  })
}

export function useCreateResource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: CreateResourceRequest) => createResource(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: resourceKeys.all }),
  })
}

export function useUpdateResourceSpec() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateResourceSpecRequest }) => updateResourceSpec(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: resourceKeys.all }),
  })
}

export function useDeleteResource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteResource(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: resourceKeys.all }),
  })
}
