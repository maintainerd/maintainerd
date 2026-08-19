import { Loader2 } from 'lucide-react'
import { BrandMark } from '@/components/brand/BrandMark'

/**
 * Full-screen splash shown while a lazy route chunk loads. Brand-static — the
 * core console has no per-tenant branding.
 */
const AppLoadingScreen = () => {
  return (
    <div
      data-console-auth-shell
      className="flex min-h-svh flex-col items-center justify-center bg-background px-4 text-foreground"
    >
      <div className="flex flex-col items-center gap-6 text-center">
        <BrandMark size={40} showTag={false} className="[&>span]:text-2xl" />
        <div className="flex items-center gap-2 text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          <span className="text-sm">Loading…</span>
        </div>
      </div>
    </div>
  )
}

export default AppLoadingScreen
