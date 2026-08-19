import { useNavigate, useParams } from 'react-router-dom'
import { Pencil } from 'lucide-react'
import { DetailsContainer } from '@/components/container/DetailsContainer'
import { FormPageHeader } from '@/components/header/FormPageHeader'
import { Button } from '@/components/ui/button'
import { DetailCard } from '@/components/core/DetailCard'
import { JsonBlock } from '@/components/core/JsonBlock'
import { StatusBadge } from '@/components/badges/StatusBadge'
import { safeFormat } from '@/lib/formatDate'
import { useProject } from '@/hooks/useProjects'
import { ResourceListingSection } from '@/pages/resources/ResourceListingSection'

export default function ProjectDetails() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: project, isLoading, error } = useProject(id)

  if (isLoading) {
    return (
      <DetailsContainer>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DetailsContainer>
    )
  }
  if (error || !project) {
    return (
      <DetailsContainer>
        <FormPageHeader backUrl="/projects" backLabel="Back to projects" title="Project not found" description="" />
      </DetailsContainer>
    )
  }

  return (
    <DetailsContainer>
      <FormPageHeader
        backUrl="/projects"
        backLabel="Back to projects"
        title={project.display_name || project.name}
        description={project.description || project.name}
        headerActions={
          <Button variant="outline" size="sm" onClick={() => navigate(`/projects/${project.project_uuid}/edit`)}>
            <Pencil className="mr-2 h-4 w-4" />
            Edit
          </Button>
        }
      />
      <div className="mt-6 space-y-6">
        <DetailCard
          title="Overview"
          rows={[
            { label: 'Status', value: <StatusBadge status={project.status} /> },
            { label: 'Name', value: project.name },
            { label: 'Project UUID', value: <span className="font-mono text-xs">{project.project_uuid}</span> },
            { label: 'Created', value: safeFormat(project.created_at, 'MMM d, yyyy p') },
          ]}
        />
        {project.metadata && Object.keys(project.metadata).length > 0 && (
          <DetailCard title="Metadata" rows={[{ label: 'metadata', value: <JsonBlock value={project.metadata} /> }]} />
        )}
        <div className="space-y-3">
          <h2 className="text-lg font-semibold tracking-tight">Resources</h2>
          <ResourceListingSection projectUuid={project.project_uuid} />
        </div>
      </div>
    </DetailsContainer>
  )
}
