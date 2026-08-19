import type { JsonObject } from '../types'

/**
 * A resource — a declarative desired-state object reconciled by the control
 * loop. `spec` is the desired state; `status` is the observed state written back
 * by the agent. Drift = `observed_generation < generation`.
 */
export interface Resource {
  resource_uuid: string
  project_uuid: string
  provider_uuid?: string | null
  kind: string
  name: string
  state: string
  spec?: JsonObject
  status?: JsonObject
  generation: number
  observed_generation: number
  metadata?: JsonObject
  created_at: string
  updated_at: string
}

export interface ResourceListParams {
  /** Required by the API — the owning project. */
  project?: string
  page?: number
  limit?: number
  [key: string]: unknown
}

export interface CreateResourceRequest {
  project_uuid: string
  provider_uuid?: string | null
  kind: string
  name: string
  spec?: JsonObject
  metadata?: JsonObject
}

/** Desired-state edit — bumps `generation` and re-arms the reconciler. */
export interface UpdateResourceSpecRequest {
  spec?: JsonObject
  metadata?: JsonObject
}
