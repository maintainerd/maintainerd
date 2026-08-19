import { useNavigate, useParams } from 'react-router-dom'
import { Pencil } from 'lucide-react'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DetailCard } from '@/components/core/DetailCard'
import { JsonBlock } from '@/components/core/JsonBlock'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { safeFormat } from '@/lib/formatDate'
import { useAgent } from '@/hooks/useAgents'

export default function AgentDetails() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: agent, isLoading, error } = useAgent(id)

  if (isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }
  if (error || !agent) {
    return (
      <DetailsContainer>
        <FormPageHeader backUrl="/agents" backLabel="Back to agents" title="Agent not found" description="" />
      </DetailsContainer>
    )
  }

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl="/agents"
        backLabel="Back to agents"
        title={agent.name}
        description={agent.endpoint || ''}
        headerActions={
          <Button variant="outline" size="sm" onClick={() => navigate(`/agents/${agent.agent_uuid}/edit`)}>
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </Button>
        }
      />
      <div className="mt-6 space-y-6">
        <DetailCard
          title="Overview"
          rows={[
            { label: 'Status', value: <StatusBadge status={agent.status} /> },
            { label: 'Endpoint', value: agent.endpoint ? <span className="font-mono text-xs">{agent.endpoint}</span> : '—' },
            { label: 'Version', value: agent.version || '—' },
            {
              label: 'Capabilities',
              value:
                agent.capabilities && agent.capabilities.length > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {agent.capabilities.map((c) => (
                      <Badge key={c} variant="outline">
                        {c}
                      </Badge>
                    ))}
                  </div>
                ) : (
                  '—'
                ),
            },
            { label: 'Last seen', value: safeFormat(agent.last_seen_at, 'MMM d, yyyy p') },
            { label: 'Agent UUID', value: <span className="font-mono text-xs">{agent.agent_uuid}</span> },
            { label: 'Created', value: safeFormat(agent.created_at, 'MMM d, yyyy p') },
          ]}
        />
        <DetailCard title="Metadata" rows={[{ label: 'metadata', value: <JsonBlock value={agent.metadata} /> }]} />
      </div>
    </DetailsContainer>
  )
}
