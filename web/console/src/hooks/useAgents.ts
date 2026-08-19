import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listAgents, getAgent, createAgent, updateAgent, deleteAgent } from '@/services/api/agents'
import type { AgentListParams, CreateAgentRequest, UpdateAgentRequest } from '@/services/api/agents/types'
import { useCoreTenant } from '@/context/CoreTenantContext'

export const agentKeys = {
  all: ['agents'] as const,
  lists: () => [...agentKeys.all, 'list'] as const,
  list: (p?: AgentListParams) => [...agentKeys.lists(), p] as const,
  details: () => [...agentKeys.all, 'detail'] as const,
  detail: (id: string) => [...agentKeys.details(), id] as const,
}

/** Tenant-scoped list hook shaped for the data-table engine. */
export function useAgents(params?: AgentListParams) {
  const { tenantUuid } = useCoreTenant()
  return useQuery({
    queryKey: agentKeys.list({ ...params, tenant: tenantUuid }),
    queryFn: async () => {
      if (!tenantUuid) return { rows: [], total: 0 }
      const { items, total } = await listAgents({ ...params, tenant: tenantUuid })
      return { rows: items, total }
    },
    enabled: !!tenantUuid,
    placeholderData: keepPreviousData,
  })
}

export function useAgent(id?: string) {
  return useQuery({ queryKey: agentKeys.detail(id ?? ''), queryFn: () => getAgent(id!), enabled: !!id })
}

export function useCreateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: CreateAgentRequest) => createAgent(data),
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.all }),
  })
}

export function useUpdateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateAgentRequest }) => updateAgent(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.all }),
  })
}

export function useDeleteAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteAgent(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.all }),
  })
}
