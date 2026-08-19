import { get, post, patch, deleteRequest } from '../client'
import { unwrap, assertSuccess } from '../_lib/unwrap'
import { buildQuery } from '../_lib/query'
import { API_ENDPOINTS } from '../config'
import type { ApiResponse, CoreListResult } from '../types'
import type { Project, ProjectListParams, CreateProjectRequest, UpdateProjectRequest } from './types'

const base = API_ENDPOINTS.PROJECTS

export function listProjects(params: ProjectListParams): Promise<CoreListResult<Project>> {
  return get<ApiResponse<CoreListResult<Project>>>(`${base}${buildQuery(params)}`).then((r) => unwrap(r, 'fetch projects'))
}

export function getProject(uuid: string): Promise<Project> {
  return get<ApiResponse<Project>>(`${base}/${uuid}`).then((r) => unwrap(r, 'fetch project'))
}

export function createProject(data: CreateProjectRequest): Promise<Project> {
  return post<ApiResponse<Project>>(base, data).then((r) => unwrap(r, 'create project'))
}

export function updateProject(uuid: string, data: UpdateProjectRequest): Promise<Project> {
  return patch<ApiResponse<Project>>(`${base}/${uuid}`, data).then((r) => unwrap(r, 'update project'))
}

export function deleteProject(uuid: string): Promise<void> {
  return deleteRequest<ApiResponse<void>>(`${base}/${uuid}`).then((r) => assertSuccess(r, 'delete project'))
}
