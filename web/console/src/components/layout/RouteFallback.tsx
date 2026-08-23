import { Skeleton } from '@/components/ui/skeleton'

/**
 * Content-region placeholder shown while a lazy route chunk downloads.
 *
 * Deliberately NOT AppLoadingScreen: that is a full-viewport brand splash sized
 * for `min-h-svh`, and rendering it inside the shell would blank the app around
 * a sidebar that is still there. This traces the shape every page under this
 * layout opens with — a header block, a toolbar row, then rows — so the incoming
 * page settles into the skeleton instead of displacing it.
 *
 * `aria-hidden` because the region is already announced as busy by the live
 * region below; a screen reader gains nothing from the decorative bars.
 */
export function RouteFallback() {
  return (
    <div className="space-y-6" data-console-route-fallback>
      <span className="sr-only" role="status" aria-live="polite">
        Loading page
      </span>
      <div aria-hidden="true" className="space-y-6">
        <div className="space-y-2">
          <Skeleton className="h-7 w-56" />
          <Skeleton className="h-4 w-80" />
        </div>
        <div className="flex items-center justify-between gap-3">
          <Skeleton className="h-9 w-72" />
          <Skeleton className="h-9 w-32" />
        </div>
        <div className="space-y-px overflow-hidden rounded-[3px] border border-border">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full rounded-none" />
          ))}
        </div>
      </div>
    </div>
  )
}
