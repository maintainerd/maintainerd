/**
 * API Client
 * Base HTTP client for the core REST API — error handling, timeouts, verbs.
 *
 * It attaches the in-memory bearer token, normalizes every failure into an
 * `ApiError`, and turns an expired session into a re-authentication rather than
 * a page full of empty tables.
 *
 * IT NEVER LOGS A REQUEST OR RESPONSE BODY, and never the Authorization header.
 * A `console.log(error.config)` here would put a live control-plane token in the
 * browser console and in any error-reporting SDK that hooks it. Only status codes
 * and the server's own message ever reach a log line.
 */

import axios, { type AxiosError, type AxiosRequestConfig } from 'axios'
import { API_CONFIG } from './config'
import { clearAccessToken, getAccessToken } from '@/auth/tokenStore'

// Custom error class
export class ApiError extends Error {
  public status: number
  public code?: string
  public retryAfter?: number
  public responseData?: {
    error: string | object
    details?: string | object
    success?: boolean
  }

  constructor({ message, status, code, retryAfter }: { message: string; status: number; code?: string; retryAfter?: number }) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

// Maps an HTTP status to a distinct, user-facing message. Never surface the raw
// `HTTP <status>` string to users — it leaks nothing useful and reads like a bug.
function friendlyMessageForStatus(status: number): string {
  switch (status) {
    case 400:
      return 'The request was invalid. Please check your input and try again.'
    case 401:
      return 'You are not authorized to perform this action.'
    case 403:
      return 'You do not have permission to perform this action.'
    case 404:
      return 'The requested resource could not be found.'
    case 409:
      return 'This action conflicts with the current state. Please refresh and try again.'
    case 422:
      return 'Some of the information provided was invalid. Please review and try again.'
    case 429:
      return 'Too many requests. Please wait a moment and try again.'
    default:
      if (status >= 500) return 'The server ran into a problem. Please try again in a moment.'
      return 'Something went wrong. Please try again.'
  }
}

// Parses a `Retry-After` header (delta-seconds or an HTTP date) into seconds.
function parseRetryAfter(value: unknown): number | undefined {
  if (typeof value !== 'string' || value.trim() === '') return undefined
  const seconds = Number(value)
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds)
  const dateMs = Date.parse(value)
  if (!Number.isNaN(dateMs)) {
    const delta = Math.ceil((dateMs - Date.now()) / 1000)
    return delta > 0 ? delta : 0
  }
  return undefined
}

/**
 * What to do when the session is gone.
 *
 * Registered by `AuthProvider` so the router — not this module — decides where
 * an unauthenticated user belongs. Until it is registered (during boot) a 401 is
 * simply reported: bootstrap owns routing at that point, and redirecting on a
 * verdict that is still being determined is the classic reload loop.
 */
type UnauthenticatedHandler = () => void
let onUnauthenticated: UnauthenticatedHandler | null = null
// Fire once per page load. The dashboard fans out and several requests can 401
// together; without this each would trigger its own navigation and they'd race.
// This latch dies with the page, so it bounds the fan-out within one load only —
// the across-load loop is bounded by the attempt budget in `auth/oauthFlow.ts`.
let reauthStarted = false

export function setUnauthenticatedHandler(handler: UnauthenticatedHandler | null): void {
  onUnauthenticated = handler
  reauthStarted = false
}

// Create axios instance with default configuration
const axiosInstance = axios.create({
  baseURL: API_CONFIG.BASE_URL,
  timeout: API_CONFIG.TIMEOUT,
  headers: API_CONFIG.HEADERS,
})

// Attach the bearer token. It is read fresh on every request rather than baked
// into the instance's defaults, because it lives only in memory and can be
// replaced by a silent re-authorization at any moment. No token is a valid
// request to make: core's guard is dev-open in the development stack and serves
// it, and if it is not, the 401 below is what tells us so.
axiosInstance.interceptors.request.use((config) => {
  const token = getAccessToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Response interceptor: normalize every failure into an ApiError carrying a
// user-facing message and the backend's `details` (field validation map).
axiosInstance.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    if (error.response) {
      const status = error.response.status
      const data = error.response.data as {
        error?: string
        details?: string | object
        success?: boolean
        code?: string
      } | undefined

      // A 401 means the token is missing, gone or expired. The console holds no
      // refresh token by design, so recovery is a hosted-identity
      // re-authorization — handled by AuthProvider, which can do it silently
      // while the SSO session is still alive, and which reports a dead end
      // instead of redirecting when it cannot.
      if (status === 401) {
        clearAccessToken()
        if (onUnauthenticated && !reauthStarted) {
          reauthStarted = true
          onUnauthenticated()
        }
      }

      const retryAfter = status === 429
        ? parseRetryAfter(error.response.headers?.['retry-after'])
        : undefined

      // Prefer a meaningful message from the backend; otherwise fall back to a
      // distinct per-status message. Never expose the raw `HTTP <status>` text.
      const backendMessage = typeof data?.error === 'string' && data.error.trim() !== '' ? data.error : undefined
      let errorMessage = backendMessage || friendlyMessageForStatus(status)
      if (status === 429 && !backendMessage && retryAfter && retryAfter > 0) {
        errorMessage = `Too many requests. Please try again in ${retryAfter} second${retryAfter === 1 ? '' : 's'}.`
      }
      const errorDetails = data?.details || undefined

      const apiError = new ApiError({
        message: errorMessage,
        status,
        code: data?.code,
        retryAfter,
      })
      apiError.responseData = {
        error: errorMessage,
        details: errorDetails,
        success: data?.success,
      }
      throw apiError
    } else if (error.code === 'ECONNABORTED') {
      throw new ApiError({ message: 'Request timeout', status: 408, code: 'TIMEOUT' })
    } else if (error.request) {
      throw new ApiError({ message: error.message || 'Network error', status: 0, code: 'NETWORK_ERROR' })
    } else {
      throw new ApiError({ message: error.message || 'Unknown error occurred', status: 0, code: 'UNKNOWN_ERROR' })
    }
  },
)

/** HTTP GET */
export async function get<T>(endpoint: string, config?: AxiosRequestConfig): Promise<T> {
  const response = await axiosInstance.get<T>(endpoint, config)
  return response.data || ({ success: true, message: 'Request completed successfully' } as T)
}

/**
 * HTTP POST — defaults the body to `{}` so axios keeps the JSON Content-Type
 * header (it strips it when there is no body), which the backend requires.
 */
export async function post<T>(endpoint: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  const response = await axiosInstance.post<T>(endpoint, data ?? {}, config)
  return response.data || ({ success: true, message: 'Request completed successfully' } as T)
}

/** HTTP PUT — defaults the body to `{}` (see `post`). */
export async function put<T>(endpoint: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  const response = await axiosInstance.put<T>(endpoint, data ?? {}, config)
  return response.data || ({ success: true, message: 'Request completed successfully' } as T)
}

/** HTTP PATCH — defaults the body to `{}` (see `post`). */
export async function patch<T>(endpoint: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  const response = await axiosInstance.patch<T>(endpoint, data ?? {}, config)
  return response.data || ({ success: true, message: 'Request completed successfully' } as T)
}

/** HTTP DELETE */
export async function deleteRequest<T>(endpoint: string, config?: AxiosRequestConfig): Promise<T> {
  const response = await axiosInstance.delete<T>(endpoint, config)
  return response.data || ({ success: true, message: 'Request completed successfully' } as T)
}

// Convenience object.
export const apiClient = {
  get,
  post,
  put,
  patch,
  delete: deleteRequest,
}
