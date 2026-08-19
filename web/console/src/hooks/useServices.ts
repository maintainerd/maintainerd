import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listServices, getService, createService, updateService, deleteService } from '@/services/api/services'
import type { ServiceListParams, CreateServiceRequest, UpdateServiceRequest } from '@/services/api/services/types'
import { useCoreTenant } from '@/context/CoreTenantContext'

export const serviceKeys = {
  all: ['services'] as const,
  lists: () => [...serviceKeys.all, 'list'] as const,
  list: (p?: ServiceListParams) => [...serviceKeys.lists(), p] as const,
  details: () => [...serviceKeys.all, 'detail'] as const,
  detail: (id: string) => [...serviceKeys.details(), id] as const,
}

/** Tenant-scoped list hook shaped for the data-table engine. */
export function useServices(params?: ServiceListParams) {
  const { tenantUuid } = useCoreTenant()
  return useQuery({
    queryKey: serviceKeys.list({ ...params, tenant: tenantUuid }),
    queryFn: async () => {
      if (!tenantUuid) return { rows: [], total: 0 }
      const { items, total } = await listServices({ ...params, tenant: tenantUuid })
      return { rows: items, total }
    },
    enabled: !!tenantUuid,
    placeholderData: keepPreviousData,
  })
}

export function useService(id?: string) {
  return useQuery({ queryKey: serviceKeys.detail(id ?? ''), queryFn: () => getService(id!), enabled: !!id })
}

export function useCreateService() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: CreateServiceRequest) => createService(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: serviceKeys.all }),
  })
}

export function useUpdateService() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateServiceRequest }) => updateService(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: serviceKeys.all }),
  })
}

export function useDeleteService() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteService(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: serviceKeys.all }),
  })
}
