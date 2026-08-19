import { get, post, patch, deleteRequest } from '../client'
import { unwrap, assertSuccess } from '../_lib/unwrap'
import { buildQuery } from '../_lib/query'
import { API_ENDPOINTS } from '../config'
import type { ApiResponse, CoreListResult } from '../types'
import type { Agent, AgentListParams, CreateAgentRequest, UpdateAgentRequest } from './types'

const base = API_ENDPOINTS.AGENTS

export function listAgents(params: AgentListParams): Promise<CoreListResult<Agent>> {
  return get<ApiResponse<CoreListResult<Agent>>>(`${base}${buildQuery(params)}`).then((r) => unwrap(r, 'fetch agents'))
}

export function getAgent(uuid: string): Promise<Agent> {
  return get<ApiResponse<Agent>>(`${base}/${uuid}`).then((r) => unwrap(r, 'fetch agent'))
}

export function createAgent(data: CreateAgentRequest): Promise<Agent> {
  return post<ApiResponse<Agent>>(base, data).then((r) => unwrap(r, 'create agent'))
}

export function updateAgent(uuid: string, data: UpdateAgentRequest): Promise<Agent> {
  return patch<ApiResponse<Agent>>(`${base}/${uuid}`, data).then((r) => unwrap(r, 'update agent'))
}

export function deleteAgent(uuid: string): Promise<void> {
  return deleteRequest<ApiResponse<void>>(`${base}/${uuid}`).then((r) => assertSuccess(r, 'delete agent'))
}
