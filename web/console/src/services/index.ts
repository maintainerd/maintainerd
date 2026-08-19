/**
 * Services Index
 * Central export point for the core API client + shared types.
 *
 * Per-domain modules are imported directly from `@/services/api/<domain>`.
 */

export { ApiError, get, post, put, patch, deleteRequest, apiClient } from './api/client'
export { API_CONFIG, API_ENDPOINTS } from './api/config'
export type * from './api/types'
