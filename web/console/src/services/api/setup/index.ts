import { get, post } from '../client'
import { unwrap } from '../_lib/unwrap'
import type { ApiResponse } from '../types'
import type { SetupStatus, SetupResult, RunSetupRequest } from './types'

const base = '/setup'

export function getSetupStatus(): Promise<SetupStatus> {
  return get<ApiResponse<SetupStatus>>(`${base}/status`).then((r) => unwrap(r, 'fetch setup status'))
}

export function runSetup(data: RunSetupRequest): Promise<SetupResult> {
  return post<ApiResponse<SetupResult>>(base, data).then((r) => unwrap(r, 'run setup'))
}
