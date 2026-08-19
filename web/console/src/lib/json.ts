import type { JsonObject } from '@/services/api/types'

/**
 * Parses a textarea value into a JSON object for a `spec`/`config`/`metadata`
 * field. An empty string yields `undefined` (the field is omitted). Throws a
 * friendly Error when the text isn't a JSON object so the form can surface it.
 */
export function parseJsonObject(text: string, fieldLabel = 'JSON'): JsonObject | undefined {
  const trimmed = text.trim()
  if (trimmed === '') return undefined
  let value: unknown
  try {
    value = JSON.parse(trimmed)
  } catch {
    throw new Error(`${fieldLabel} must be valid JSON.`)
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${fieldLabel} must be a JSON object (e.g. { "key": "value" }).`)
  }
  return value as JsonObject
}

/** Pretty-prints a JSON object for display / editing, or '' when empty. */
export function stringifyJson(value?: JsonObject | null): string {
  if (value == null || Object.keys(value).length === 0) return ''
  return JSON.stringify(value, null, 2)
}
