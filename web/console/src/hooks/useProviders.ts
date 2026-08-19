import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listProviders, getProvider, createProvider, updateProvider, deleteProvider } from '@/services/api/providers'
import type { ProviderListParams, CreateProviderRequest, UpdateProviderRequest } from '@/services/api/providers/types'
import { useCoreTenant } from '@/context/CoreTenantContext'

export const providerKeys = {
  all: ['providers'] as const,
  lists: () => [...providerKeys.all, 'list'] as const,
  list: (p?: ProviderListParams) => [...providerKeys.lists(), p] as const,
  details: () => [...providerKeys.all, 'detail'] as const,
  detail: (id: string) => [...providerKeys.details(), id] as const,
}

/** Tenant-scoped list hook shaped for the data-table engine. */
export function useProviders(params?: ProviderListParams) {
  const { tenantUuid } = useCoreTenant()
  return useQuery({
    queryKey: providerKeys.list({ ...params, tenant: tenantUuid }),
    queryFn: async () => {
      if (!tenantUuid) return { rows: [], total: 0 }
      const { items, total } = await listProviders({ ...params, tenant: tenantUuid })
      return { rows: items, total }
    },
    enabled: !!tenantUuid,
    placeholderData: keepPreviousData,
  })
}

export function useProvider(id?: string) {
  return useQuery({ queryKey: providerKeys.detail(id ?? ''), queryFn: () => getProvider(id!), enabled: !!id })
}

export function useCreateProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: CreateProviderRequest) => createProvider(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: providerKeys.all }),
  })
}

export function useUpdateProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateProviderRequest }) => updateProvider(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: providerKeys.all }),
  })
}

export function useDeleteProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteProvider(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: providerKeys.all }),
  })
}
