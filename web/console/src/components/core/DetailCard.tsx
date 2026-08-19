import type { ReactNode } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export interface DetailRow {
  label: string
  value: ReactNode
}

/** A simple titled card rendering a label/value definition list for detail pages. */
export function DetailCard({
  title,
  rows,
  action,
}: {
  title: string
  rows: DetailRow[]
  action?: ReactNode
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0">
        <CardTitle className="text-base font-semibold">{title}</CardTitle>
        {action}
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
          {rows.map((row) => (
            <div key={row.label} className="min-w-0">
              <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{row.label}</dt>
              <dd className="mt-1 break-words text-sm text-foreground">{row.value ?? '—'}</dd>
            </div>
          ))}
        </dl>
      </CardContent>
    </Card>
  )
}
