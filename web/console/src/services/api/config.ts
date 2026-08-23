/**
 * API + identity configuration.
 * Centralized configuration for the core control-plane API.
 *
 * Every value can be supplied at RUN time (window.__ENV__, from public/config.js)
 * or at BUILD time (import.meta.env), so one built image targets several
 * deployments. Nothing here is a secret: the console is a public OAuth client
 * (authorization code + PKCE), so it has no client secret to leak.
 */

// Runtime environment injected into window.__ENV__ before the app bundle loads.
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

/**
 * Reads one setting, trying each key in order and taking the first that is set,
 * runtime before build time.
 *
 * TWO SPELLINGS ON PURPOSE. The first key is the name core's own process uses
 * for the same fact — `AUTH_ISSUER`, `AUTH_AUDIENCE`, `CORE_CONSOLE_CLIENT_ID` —
 * and the later ones are the `VITE_*` build-time names. An operator creates one
 * console SPA client in maintainerd-auth and is handed one client id; a console
 * whose variable were spelled differently from the service's would be an
 * invitation to set one and not the other, and the symptom of that — an
 * authorize request for a client id that does not exist — surfaces in a browser,
 * far from the operator reading the runbook.
 */
function setting(...keys: string[]): string {
  for (const key of keys) {
    const value = runtimeEnv(key) ?? (import.meta.env[key] as string | undefined)
    if (value && value.trim() !== '') return value.trim()
  }
  return ''
}

function trimSlash(value: string): string {
  return value.replace(/\/$/, '')
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

/**
 * The identity configuration, or null when this deployment has none.
 *
 * NULL IS A REAL, SUPPORTED STATE and not an error. Core runs with
 * APP_ENV=development and no AUTH_JWKS_URL/AUTH_ISSUER/AUTH_AUDIENCE in the dev
 * stack, which puts its HTTP guard in dev-open mode: it serves every caller as a
 * blanket-granted principal and there is no token to obtain.
 *
 * NULL IS NOT A CLAIM ABOUT THE SERVER. Unlike maintainerd-secret, core exposes
 * no `/capabilities` probe, so this console cannot ask whether the guard is
 * enforced (see `auth/AuthProvider.tsx`). A missing identity config therefore
 * means only "this console cannot obtain a token" — never "no token is needed".
 * Which of those it is gets settled by the API's own answer: a 401 proves the
 * guard is enforced, and requests that succeed prove it is open.
 *
 * All three of issuer/token/client are required TOGETHER. A half-configured
 * identity would send the operator to an authorize endpoint whose code could
 * never be exchanged.
 */
export interface IdentityConfig {
  /** Hosted identity origin — `/authorize` and `/end-session` hang off it. */
  issuerUrl: string
  /** Absolute URL of the OAuth token endpoint (maintainerd-auth public API). */
  tokenUrl: string
  /** This console's public OAuth client id. */
  clientId: string
  /** Resource-API audience core enforces (its AUTH_AUDIENCE). */
  audience: string
  /** Space-separated scopes requested on top of `openid profile email`. */
  extraScope: string
}

/**
 * The runtime keys an operator sets to make this console able to sign in, in the
 * order they are reported when it cannot. Kept next to `readIdentityConfig` so
 * the error surface and the reader can never drift apart.
 */
export const IDENTITY_SETTING_KEYS = [
  'AUTH_ISSUER',
  'CORE_CONSOLE_TOKEN_URL',
  'CORE_CONSOLE_CLIENT_ID',
] as const

function readIdentityConfig(): IdentityConfig | null {
  // Each line lists the SERVICE's spelling first and the build-time VITE_ name
  // second — see `setting`. The issuer is Auth's hosted identity origin, which is
  // the same value core enforces as AUTH_ISSUER.
  const issuerUrl = trimSlash(setting('AUTH_ISSUER', 'VITE_OAUTH_ISSUER_URL'))
  const tokenUrl = setting('CORE_CONSOLE_TOKEN_URL', 'VITE_OAUTH_TOKEN_URL')
  const clientId = setting('CORE_CONSOLE_CLIENT_ID', 'VITE_OAUTH_CLIENT_ID')
  if (!issuerUrl || !tokenUrl || !clientId) return null
  return {
    issuerUrl,
    tokenUrl,
    clientId,
    audience: setting('AUTH_AUDIENCE', 'VITE_OAUTH_AUDIENCE'),
    extraScope: setting('CORE_CONSOLE_SCOPE', 'VITE_OAUTH_SCOPE'),
  }
}

export const IDENTITY_CONFIG: IdentityConfig | null = readIdentityConfig()

export const API_CONFIG = {
  BASE_URL: getBaseUrl(),
  TIMEOUT: 30000, // 30 seconds
  HEADERS: {
    'Content-Type': 'application/json',
  },
} as const

/**
 * The header the setup surface's own gate reads — core's `setupTokenHeader`
 * (internal/setup/handler.go), the HTTP twin of the `x-setup-token` gRPC metadata
 * key Auth and Secret take the same credential in.
 *
 * A HEADER AND NEVER A QUERY PARAMETER. The value is core's `CORE_SETUP_TOKEN`
 * and that endpoint creates the first tenant and admin; in a URL it would land in
 * browser history, in the referer of every request that followed, and in every
 * proxy access log between here and core.
 */
export const SETUP_TOKEN_HEADER = 'X-Setup-Token'

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
