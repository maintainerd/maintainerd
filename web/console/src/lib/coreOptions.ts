import type { SelectOption } from '@/components/form'

/** Status values a tenant may hold (matches the core tenant status set). */
export const TENANT_STATUS_OPTIONS: SelectOption[] = [
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'suspended', label: 'Suspended' },
]

/** Generic lifecycle status set shared by projects/services/providers/agents. */
export const LIFECYCLE_STATUS_OPTIONS: SelectOption[] = [
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'pending', label: 'Pending' },
  { value: 'maintenance', label: 'Maintenance' },
  { value: 'suspended', label: 'Suspended' },
]
