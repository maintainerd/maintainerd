import { Fragment } from 'react'
import { Link } from 'react-router-dom'
import { ChevronRight, Home } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface Crumb {
  label: string
  /** When set, the crumb is a link; the last crumb is usually plain text. */
  to?: string
}

/**
 * Breadcrumb trail for detail/form pages so the operator always knows where they
 * are and can jump back up the hierarchy (Dashboard ▸ Projects ▸ default ▸ …).
 */
export function Breadcrumbs({ items, className }: { items: Crumb[]; className?: string }) {
  return (
    <nav aria-label="Breadcrumb" className={cn('flex items-center gap-1 text-sm text-muted-foreground', className)}>
      <Link to="/dashboard" className="flex items-center hover:text-foreground" aria-label="Dashboard">
        <Home className="size-3.5" />
      </Link>
      {items.map((item, i) => {
        const last = i === items.length - 1
        return (
          <Fragment key={`${item.label}-${i}`}>
            <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/60" />
            {item.to && !last ? (
              <Link to={item.to} className="truncate hover:text-foreground">
                {item.label}
              </Link>
            ) : (
              <span className={cn('truncate', last && 'font-medium text-foreground')}>{item.label}</span>
            )}
          </Fragment>
        )
      })}
    </nav>
  )
}
