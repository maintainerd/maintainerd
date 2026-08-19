import { get, post, patch, deleteRequest } from '../client'
import { unwrap, assertSuccess } from '../_lib/unwrap'
import { buildQuery } from '../_lib/query'
import { API_ENDPOINTS } from '../config'
import type { ApiResponse, CoreListResult } from '../types'
import type { Tenant, TenantListParams, CreateTenantRequest, UpdateTenantRequest } from './types'

const base = API_ENDPOINTS.TENANTS

export function listTenants(params?: TenantListParams): Promise<CoreListResult<Tenant>> {
  return get<ApiResponse<CoreListResult<Tenant>>>(`${base}${buildQuery(params)}`).then((r) => unwrap(r, 'fetch tenants'))
}

export function getSystemTenant(): Promise<Tenant> {
  return get<ApiResponse<Tenant>>(API_ENDPOINTS.TENANTS_SYSTEM).then((r) => unwrap(r, 'fetch system tenant'))
}

export function getTenant(uuid: string): Promise<Tenant> {
  return get<ApiResponse<Tenant>>(`${base}/${uuid}`).then((r) => unwrap(r, 'fetch tenant'))
}

export function createTenant(data: CreateTenantRequest): Promise<Tenant> {
  return post<ApiResponse<Tenant>>(base, data).then((r) => unwrap(r, 'create tenant'))
}

export function updateTenant(uuid: string, data: UpdateTenantRequest): Promise<Tenant> {
  return patch<ApiResponse<Tenant>>(`${base}/${uuid}`, data).then((r) => unwrap(r, 'update tenant'))
}

export function deleteTenant(uuid: string): Promise<void> {
  return deleteRequest<ApiResponse<void>>(`${base}/${uuid}`).then((r) => assertSuccess(r, 'delete tenant'))
}
