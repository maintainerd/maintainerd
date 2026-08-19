import { get, post, patch, deleteRequest } from '../client'
import { unwrap, assertSuccess } from '../_lib/unwrap'
import { buildQuery } from '../_lib/query'
import { API_ENDPOINTS } from '../config'
import type { ApiResponse, CoreListResult } from '../types'
import type { Provider, ProviderListParams, CreateProviderRequest, UpdateProviderRequest } from './types'

const base = API_ENDPOINTS.PROVIDERS

export function listProviders(params: ProviderListParams): Promise<CoreListResult<Provider>> {
  return get<ApiResponse<CoreListResult<Provider>>>(`${base}${buildQuery(params)}`).then((r) => unwrap(r, 'fetch providers'))
}

export function getProvider(uuid: string): Promise<Provider> {
  return get<ApiResponse<Provider>>(`${base}/${uuid}`).then((r) => unwrap(r, 'fetch provider'))
}

export function createProvider(data: CreateProviderRequest): Promise<Provider> {
  return post<ApiResponse<Provider>>(base, data).then((r) => unwrap(r, 'create provider'))
}

export function updateProvider(uuid: string, data: UpdateProviderRequest): Promise<Provider> {
  return patch<ApiResponse<Provider>>(`${base}/${uuid}`, data).then((r) => unwrap(r, 'update provider'))
}

export function deleteProvider(uuid: string): Promise<void> {
  return deleteRequest<ApiResponse<void>>(`${base}/${uuid}`).then((r) => assertSuccess(r, 'delete provider'))
}
