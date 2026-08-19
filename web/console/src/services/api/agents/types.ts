import type { JsonObject } from '../types'

/** An agent — the executor that pulls work from core and runs it. */
export interface Agent {
  agent_uuid: string
  tenant_uuid: string
  name: string
  status: string
  endpoint?: string
  version?: string
  capabilities?: string[]
  last_seen_at?: string
  metadata?: JsonObject
  created_at: string
  updated_at: string
}

export interface AgentListParams {
  /** Required by the API — the owning tenant. */
  tenant?: string
  page?: number
  limit?: number
  [key: string]: unknown
}

export interface CreateAgentRequest {
  tenant_uuid: string
  name: string
  endpoint?: string
  version?: string
  capabilities?: string[]
  metadata?: JsonObject
}

export interface UpdateAgentRequest {
  status?: string
  endpoint?: string
  version?: string
  capabilities?: string[]
}
