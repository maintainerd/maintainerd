/**
 * Console branding. Static product identity for the maintainerd console, with
 * optional runtime overrides (window.__ENV__) so a deployment can white-label
 * without a rebuild — mirroring how the API base URL is injected.
 */

function runtimeEnv(key: string): string | undefined {
  if (typeof window === 'undefined') return undefined
  const v = window.__ENV__?.[key]
  return v && v.trim() !== '' ? v : undefined
}

export interface Branding {
  /** Product wordmark, e.g. "maintainerd". */
  name: string
  /** Short qualifier shown next to the wordmark, e.g. "Console". */
  tagline: string
  /** Optional logo image URL; when unset the built-in mark renders. */
  logoUrl?: string
}

export const branding: Branding = {
  name: runtimeEnv('VITE_CONSOLE_NAME') || 'maintainerd',
  tagline: runtimeEnv('VITE_CONSOLE_TAGLINE') || 'Console',
  logoUrl: runtimeEnv('VITE_CONSOLE_LOGO_URL'),
}
