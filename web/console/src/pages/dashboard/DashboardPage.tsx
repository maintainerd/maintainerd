import { useNavigate } from 'react-router-dom'
import type { LucideIcon } from 'lucide-react'
import { Building2, FolderKanban, Server, Plug, Cpu, ArrowRight } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { PageHeader } from '@/components/layout/PageHeader'
import { useCoreTenant } from '@/context/CoreTenantContext'
import { useProjects } from '@/hooks/useProjects'
import { useServices } from '@/hooks/useServices'
import { useProviders } from '@/hooks/useProviders'
import { useAgents } from '@/hooks/useAgents'

function StatCard({
  label,
  value,
  isLoading,
  icon: Icon,
  onClick,
}: {
  label: string
  value: number
  isLoading: boolean
  icon: LucideIcon
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="group text-left"
    >
      <Card className="transition-colors hover:border-primary/40">
        <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0 pb-2">
          <CardTitle className="text-sm font-medium text-muted-foreground">{label}</CardTitle>
          <Icon className="size-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <div className="flex items-end justify-between">
            <span className="text-3xl font-semibold tracking-tight">{isLoading ? '—' : value}</span>
            <ArrowRight className="size-4 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
          </div>
        </CardContent>
      </Card>
    </button>
  )
}

export default function DashboardPage() {
  const navigate = useNavigate()
  const { tenant, tenants, isLoading: tenantLoading } = useCoreTenant()

  const projects = useProjects({ limit: 1 })
  const services = useServices({ limit: 1 })
  const providers = useProviders({ limit: 1 })
  const agents = useAgents({ limit: 1 })

  const noTenants = !tenantLoading && tenants.length === 0

  return (
    <div className="space-y-6">
      <PageHeader
        title="Dashboard"
        description={tenant ? `Overview of ${tenant.display_name || tenant.name}.` : 'Your maintainerd control plane.'}
      />

      {noTenants ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base font-semibold">Get started</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              No tenants exist yet. Create your first tenant to start declaring projects, providers, agents and
              resources.
            </p>
            <button
              type="button"
              onClick={() => navigate('/tenants/create')}
              className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
            >
              <Building2 className="size-4" />
              Create a tenant
            </button>
          </CardContent>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard
            label="Projects"
            value={projects.data?.total ?? 0}
            isLoading={projects.isLoading}
            icon={FolderKanban}
            onClick={() => navigate('/projects')}
          />
          <StatCard
            label="Services"
            value={services.data?.total ?? 0}
            isLoading={services.isLoading}
            icon={Server}
            onClick={() => navigate('/services')}
          />
          <StatCard
            label="Providers"
            value={providers.data?.total ?? 0}
            isLoading={providers.isLoading}
            icon={Plug}
            onClick={() => navigate('/providers')}
          />
          <StatCard
            label="Agents"
            value={agents.data?.total ?? 0}
            isLoading={agents.isLoading}
            icon={Cpu}
            onClick={() => navigate('/agents')}
          />
        </div>
      )}
    </div>
  )
}
