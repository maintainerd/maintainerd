import { get, post, patch, deleteRequest } from '../client'
import { unwrap, assertSuccess } from '../_lib/unwrap'
import { buildQuery } from '../_lib/query'
import { API_ENDPOINTS } from '../config'
import type { ApiResponse, CoreListResult } from '../types'
import type { Resource, ResourceListParams, CreateResourceRequest, UpdateResourceSpecRequest } from './types'

const base = API_ENDPOINTS.RESOURCES

export function listResources(params: ResourceListParams): Promise<CoreListResult<Resource>> {
  return get<ApiResponse<CoreListResult<Resource>>>(`${base}${buildQuery(params)}`).then((r) => unwrap(r, 'fetch resources'))
}

export function getResource(uuid: string): Promise<Resource> {
  return get<ApiResponse<Resource>>(`${base}/${uuid}`).then((r) => unwrap(r, 'fetch resource'))
}

export function createResource(data: CreateResourceRequest): Promise<Resource> {
  return post<ApiResponse<Resource>>(base, data).then((r) => unwrap(r, 'create resource'))
}

export function updateResourceSpec(uuid: string, data: UpdateResourceSpecRequest): Promise<Resource> {
  return patch<ApiResponse<Resource>>(`${base}/${uuid}`, data).then((r) => unwrap(r, 'update resource'))
}

export function deleteResource(uuid: string): Promise<void> {
  return deleteRequest<ApiResponse<void>>(`${base}/${uuid}`).then((r) => assertSuccess(r, 'delete resource'))
}
