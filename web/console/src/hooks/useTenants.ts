import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listTenants,
  getTenant,
  getSystemTenant,
  createTenant,
  updateTenant,
  deleteTenant,
} from '@/services/api/tenants'
import type { TenantListParams, CreateTenantRequest, UpdateTenantRequest } from '@/services/api/tenants/types'

export const tenantKeys = {
  all: ['tenants'] as const,
  lists: () => [...tenantKeys.all, 'list'] as const,
  list: (p?: TenantListParams) => [...tenantKeys.lists(), p] as const,
  details: () => [...tenantKeys.all, 'detail'] as const,
  detail: (id: string) => [...tenantKeys.details(), id] as const,
  system: () => [...tenantKeys.all, 'system'] as const,
}

/** List hook shaped for the data-table engine: `{ data: { rows, total } }`. */
export function useTenants(params?: TenantListParams) {
  return useQuery({
    queryKey: tenantKeys.list(params),
    queryFn: async () => {
      const { items, total } = await listTenants(params)
      return { rows: items, total }
    },
    placeholderData: keepPreviousData,
  })
}

export function useTenant(id?: string) {
  return useQuery({ queryKey: tenantKeys.detail(id ?? ''), queryFn: () => getTenant(id!), enabled: !!id })
}

export function useSystemTenant() {
  return useQuery({ queryKey: tenantKeys.system(), queryFn: getSystemTenant, retry: false })
}

export function useCreateTenant() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: CreateTenantRequest) => createTenant(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: tenantKeys.all }),
  })
}

export function useUpdateTenant() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateTenantRequest }) => updateTenant(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: tenantKeys.all }),
  })
}

export function useDeleteTenant() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteTenant(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: tenantKeys.all }),
  })
}
