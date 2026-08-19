import { createContext, useContext, type ReactNode } from 'react'

interface ResourceScopeValue {
  /** The project the resource listing is scoped to (API requires `?project=`). */
  projectUuid: string
}

const ResourceScopeContext = createContext<ResourceScopeValue | undefined>(undefined)

/** Provides the current project UUID to a nested resource listing. */
export function ResourceScopeProvider({ projectUuid, children }: { projectUuid: string; children: ReactNode }) {
  return <ResourceScopeContext.Provider value={{ projectUuid }}>{children}</ResourceScopeContext.Provider>
}

export function useResourceScope(): ResourceScopeValue {
  const ctx = useContext(ResourceScopeContext)
  if (!ctx) throw new Error('useResourceScope must be used within a ResourceScopeProvider')
  return ctx
}
