import { get, post, patch, deleteRequest } from '../client'
import { unwrap, assertSuccess } from '../_lib/unwrap'
import { buildQuery } from '../_lib/query'
import { API_ENDPOINTS } from '../config'
import type { ApiResponse, CoreListResult } from '../types'
import type {
  ServiceRegistration,
  ServiceListParams,
  CreateServiceRequest,
  UpdateServiceRequest,
} from './types'

const base = API_ENDPOINTS.SERVICES

export function listServices(params: ServiceListParams): Promise<CoreListResult<ServiceRegistration>> {
  return get<ApiResponse<CoreListResult<ServiceRegistration>>>(`${base}${buildQuery(params)}`).then((r) =>
    unwrap(r, 'fetch services'),
  )
}

export function listSystemServices(): Promise<CoreListResult<ServiceRegistration>> {
  return get<ApiResponse<CoreListResult<ServiceRegistration>>>(API_ENDPOINTS.SERVICES_SYSTEM).then((r) =>
    unwrap(r, 'fetch system services'),
  )
}

export function getService(uuid: string): Promise<ServiceRegistration> {
  return get<ApiResponse<ServiceRegistration>>(`${base}/${uuid}`).then((r) => unwrap(r, 'fetch service'))
}

export function createService(data: CreateServiceRequest): Promise<ServiceRegistration> {
  return post<ApiResponse<ServiceRegistration>>(base, data).then((r) => unwrap(r, 'create service'))
}

export function updateService(uuid: string, data: UpdateServiceRequest): Promise<ServiceRegistration> {
  return patch<ApiResponse<ServiceRegistration>>(`${base}/${uuid}`, data).then((r) => unwrap(r, 'update service'))
}

export function deleteService(uuid: string): Promise<void> {
  return deleteRequest<ApiResponse<void>>(`${base}/${uuid}`).then((r) => assertSuccess(r, 'delete service'))
}
