import type { JsonObject } from '../types'

/** A provider — a driver that materializes resources of a given kind. */
export interface Provider {
  provider_uuid: string
  tenant_uuid: string
  name: string
  resource_kind: string
  driver: string
  config?: JsonObject
  status: string
  metadata?: JsonObject
  created_at: string
  updated_at: string
}

export interface ProviderListParams {
  /** Required by the API — the owning tenant. */
  tenant?: string
  page?: number
  limit?: number
  [key: string]: unknown
}

export interface CreateProviderRequest {
  tenant_uuid: string
  name: string
  resource_kind: string
  driver: string
  config?: JsonObject
  metadata?: JsonObject
}

export interface UpdateProviderRequest {
  driver?: string
  config?: JsonObject
  status?: string
  metadata?: JsonObject
}
