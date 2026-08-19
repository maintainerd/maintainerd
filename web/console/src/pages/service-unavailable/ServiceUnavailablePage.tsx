import { ServerCrash } from 'lucide-react'
import { Button } from '@/components/ui/button'

const ServiceUnavailablePage = () => {
  const handleRetry = () => {
    window.location.reload()
  }

  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-8 bg-background px-4 text-center">
      <div className="flex flex-col items-center gap-3">
        <div className="flex size-14 items-center justify-center rounded-full bg-destructive/10">
          <ServerCrash className="size-7 text-destructive" />
        </div>
        <h1 className="text-2xl font-semibold tracking-tight">Service Unavailable</h1>
        <p className="max-w-xs text-sm text-muted-foreground">
          We&apos;re unable to reach the control plane right now. This is usually temporary &mdash; please wait a
          moment and try again.
        </p>
      </div>
      <Button onClick={handleRetry}>Try again</Button>
    </div>
  )
}

export default ServiceUnavailablePage
