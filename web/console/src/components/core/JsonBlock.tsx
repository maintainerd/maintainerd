import type { JsonObject } from '@/services/api/types'
import { stringifyJson } from '@/lib/json'

/** Read-only pretty-printed JSON block for spec/status/config/metadata display. */
export function JsonBlock({ value, empty = 'Empty' }: { value?: JsonObject | null; empty?: string }) {
  const text = stringifyJson(value)
  if (text === '') return <span className="text-sm text-muted-foreground">{empty}</span>
  return (
    <pre className="max-h-96 overflow-auto rounded-md border border-border bg-muted/40 p-3 text-xs leading-relaxed text-foreground">
      {text}
    </pre>
  )
}
