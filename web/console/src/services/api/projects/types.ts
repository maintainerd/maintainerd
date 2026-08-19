import type { JsonObject } from '../types'

/** A project — a workload grouping owned by a tenant. */
export interface Project {
  project_uuid: string
  tenant_uuid: string
  name: string
  display_name?: string
  description?: string
  status: string
  metadata?: JsonObject
  created_at: string
  updated_at: string
}

export interface ProjectListParams {
  /** Required by the API — the owning tenant. */
  tenant?: string
  page?: number
  limit?: number
  [key: string]: unknown
}

export interface CreateProjectRequest {
  tenant_uuid: string
  name: string
  display_name?: string
  description?: string
  metadata?: JsonObject
}

export interface UpdateProjectRequest {
  display_name?: string
  description?: string
  status?: string
  metadata?: JsonObject
}
