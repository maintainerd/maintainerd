import { useNavigate } from 'react-router-dom'
import { Compass } from 'lucide-react'
import { Button } from '@/components/ui/button'

/** Catch-all page for unknown routes. */
const NotFoundPage = () => {
  const navigate = useNavigate()

  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-8 text-center">
      <div className="flex flex-col items-center gap-3">
        <div className="flex size-14 items-center justify-center rounded-full bg-muted">
          <Compass className="size-7 text-muted-foreground" />
        </div>
        <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
        <p className="max-w-xs text-sm text-muted-foreground">
          The page you&apos;re looking for doesn&apos;t exist or may have moved.
        </p>
      </div>
      <Button onClick={() => navigate('/dashboard', { replace: true })}>Back to dashboard</Button>
    </div>
  )
}

export default NotFoundPage
