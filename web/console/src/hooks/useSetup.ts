import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSetupStatus, runSetup } from '@/services/api/setup'
import type { RunSetupRequest } from '@/services/api/setup/types'
import { tenantKeys } from '@/hooks/useTenants'

export const setupKeys = {
  status: ['setup', 'status'] as const,
}

/** Whether Core has been set up — drives the first-run wizard gate. */
export function useSetupStatus() {
  return useQuery({
    queryKey: setupKeys.status,
    queryFn: getSetupStatus,
    // Don't hammer if core is momentarily unreachable; the gate fails open.
    retry: 1,
    staleTime: 0,
  })
}

/** Runs the full provisioning (Core drives Auth) from the wizard's tenant + admin. */
export function useRunSetup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: RunSetupRequest) => runSetup(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: setupKeys.status })
      qc.invalidateQueries({ queryKey: tenantKeys.all })
    },
  })
}
