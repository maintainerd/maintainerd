import { branding } from '@/lib/branding'
import { cn } from '@/lib/utils'

/** The maintainerd logo glyph — a layered "control plane" stack in a rounded
 *  square. Uses a custom logo image when one is configured via branding. */
export function BrandLogo({ size = 28, className }: { size?: number; className?: string }) {
  if (branding.logoUrl) {
    return <img src={branding.logoUrl} alt={branding.name} width={size} height={size} className={cn('shrink-0 object-contain', className)} />
  }
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={cn('shrink-0', className)}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id="bm-g" x1="0" y1="0" x2="32" y2="32" gradientUnits="userSpaceOnUse">
          <stop stopColor="#3b82f6" />
          <stop offset="1" stopColor="#4f46e5" />
        </linearGradient>
      </defs>
      <rect width="32" height="32" rx="8" fill="url(#bm-g)" />
      {/* three offset rounded bars — the control-plane layers */}
      <rect x="8" y="9" width="16" height="3.4" rx="1.7" fill="#fff" opacity="0.95" />
      <rect x="8" y="14.3" width="12" height="3.4" rx="1.7" fill="#fff" opacity="0.75" />
      <rect x="8" y="19.6" width="8" height="3.4" rx="1.7" fill="#fff" opacity="0.55" />
    </svg>
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
