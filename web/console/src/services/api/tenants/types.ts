import type { JsonObject } from '../types'

/** A core tenant — the top-level isolation boundary. */
export interface Tenant {
  tenant_uuid: string
  auth_tenant_uuid?: string
  name: string
  display_name?: string
  status: string
  is_system: boolean
  metadata?: JsonObject
  created_at: string
  updated_at: string
}

export interface TenantListParams {
  page?: number
  limit?: number
  [key: string]: unknown
}

export interface CreateTenantRequest {
  name: string
  display_name?: string
  status?: string
  is_system?: boolean
  auth_tenant_uuid?: string
  metadata?: JsonObject
}

export interface UpdateTenantRequest {
  display_name?: string
  status?: string
  auth_tenant_uuid?: string
  metadata?: JsonObject
}
