import { branding } from '@/lib/branding'
import { cn } from '@/lib/utils'

/** The official maintainerd logo mark (two-tone blue cloud, transparent
 *  background so it sits on any surface). Overridable via branding.logoUrl. */
export function BrandLogo({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <img
      src={branding.logoUrl || '/maintainerd-mark.svg'}
      alt={branding.name}
      width={size}
      height={size}
      className={cn('shrink-0 object-contain', className)}
    />
  )
}

interface BrandMarkProps {
  /** 'full' renders the logo + wordmark; 'logo' just the glyph. */
  variant?: 'full' | 'logo'
  /** Show the small "Console" tag next to the wordmark. */
  showTag?: boolean
  size?: number
  className?: string
  /** Tailwind color class for the wordmark (defaults to current text color). */
  wordmarkClassName?: string
}

/** Brand lockup: the logo glyph + the product wordmark (+ optional tag). */
export function BrandMark({
  variant = 'full',
  showTag = true,
  size = 28,
  className,
  wordmarkClassName = 'text-foreground',
}: BrandMarkProps) {
  return (
    <span className={cn('flex items-center gap-2', className)}>
      <BrandLogo size={size} />
      {variant === 'full' && (
        <span className="flex items-baseline gap-2">
          <span className={cn('text-lg font-semibold leading-none tracking-tight', wordmarkClassName)}>
            {branding.name}
          </span>
          {showTag && (
            <span className="text-[11px] font-medium uppercase tracking-wide text-slate-400">
              {branding.tagline}
            </span>
          )}
        </span>
      )}
    </span>
  )
}
