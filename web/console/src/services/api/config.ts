/**
 * API Configuration
 * Centralized configuration for the core control-plane API.
 */

// Runtime environment injected by docker-entrypoint.sh into window.__ENV__.
// Lets a single built image target a different API origin per deployment without
// a rebuild. Values are optional; build-time import.meta.env is the fallback.
declare global {
  interface Window {
    __ENV__?: Record<string, string | undefined>
  }
}

function runtimeEnv(key: string): string | undefined {
  if (typeof window === 'undefined') return undefined
  const value = window.__ENV__?.[key]
  // Ignore empty placeholders left by the local-dev config.js.
  return value && value.trim() !== '' ? value : undefined
}

// Base URL for the core REST API.
// In development the console is served through the maintainerd-dev nginx edge,
// so it always talks to the same-origin `/api/v1` (Vite proxies it to core).
// In production the release image serves the SPA same-origin behind its own
// proxy; an absolute URL may still be injected for split-host deployments.
const getBaseUrl = () => {
  if (import.meta.env.DEV) return '/api/v1'
  return (
    runtimeEnv('VITE_CORE_API_BASE_URL') ||
    import.meta.env.VITE_CORE_API_BASE_URL ||
    '/api/v1'
  )
}

export const API_CONFIG = {
  BASE_URL: getBaseUrl(),
  TIMEOUT: 30000, // 30 seconds
  HEADERS: {
    'Content-Type': 'application/json',
  },
} as const

// Core REST endpoints (paths are relative to BASE_URL = /api/v1).
export const API_ENDPOINTS = {
  HEALTH: '/healthz',
  TENANTS: '/tenants',
  TENANTS_SYSTEM: '/tenants/system',
  PROJECTS: '/projects',
  SERVICES: '/services',
  SERVICES_SYSTEM: '/services/system',
  PROVIDERS: '/providers',
  AGENTS: '/agents',
  RESOURCES: '/resources',
} as const
