import type { JsonObject } from '../types'

/** A registered service (registration) governed by the tenant. */
export interface ServiceRegistration {
  service_uuid: string
  tenant_uuid: string
  name: string
  kind: string
  is_system: boolean
  status: string
  endpoint?: string
  version?: string
  metadata?: JsonObject
  registered_at?: string
  created_at: string
  updated_at: string
}

export interface ServiceListParams {
  /** Required by the API — the owning tenant. */
  tenant?: string
  page?: number
  limit?: number
  [key: string]: unknown
}

export interface CreateServiceRequest {
  tenant_uuid: string
  name: string
  kind: string
  is_system?: boolean
  endpoint?: string
  version?: string
  metadata?: JsonObject
}

/** The API only allows status + endpoint to be patched. */
export interface UpdateServiceRequest {
  status?: string
  endpoint?: string
}
