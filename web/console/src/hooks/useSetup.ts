import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSetupStatus, runSetup } from '@/services/api/setup'
import type { RunSetupRequest } from '@/services/api/setup/types'
import { tenantKeys } from '@/hooks/useTenants'

export const setupKeys = {
  status: ['setup', 'status'] as const,
}

/**
 * Whether Core has been set up — drives the first-run wizard gate.
 *
 * Called with NO ARGUMENT, and wrapped rather than passed by reference: react-query
 * invokes `queryFn` with a QueryFunctionContext, which `getSetupStatus` would take
 * for a setup token and send as the header value. The gate only needs the anonymous
 * `completed` bit anyway.
 */
export function useSetupStatus() {
  return useQuery({
    queryKey: setupKeys.status,
    queryFn: () => getSetupStatus(),
    // Don't hammer if core is momentarily unreachable; the gate fails CLOSED on an
    // unreadable status (see components/SetupGate.tsx), so a retry storm buys nothing.
    retry: 1,
    staleTime: 0,
  })
}

/**
 * Runs the full provisioning (Core drives Auth) from the wizard's tenant + admin.
 *
 * The bootstrap token travels ALONGSIDE the body, not inside it: it authenticates
 * the call (`X-Setup-Token`) and is not part of what gets provisioned, and keeping
 * them apart is what stops it from ever being serialized into the request body.
 */
export function useRunSetup() {
  const qc = useQueryClient()
  return useMutation({
    // OPTIONAL: a fresh install has no token to give — core is the starting
    // point, so nothing has issued one yet. See runSetup for the full argument.
    mutationFn: (input: { body: RunSetupRequest; setupToken?: string }) =>
      runSetup(input.body, input.setupToken),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: setupKeys.status })
      qc.invalidateQueries({ queryKey: tenantKeys.all })
    },
  })
}
