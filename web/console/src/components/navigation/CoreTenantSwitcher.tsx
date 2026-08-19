import { Building2, Check, ChevronsUpDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useCoreTenant } from '@/context/CoreTenantContext'

/** Top-bar selector for the active tenant every listing is scoped to. */
export function CoreTenantSwitcher() {
  const { tenant, tenants, setTenantUuid, isLoading } = useCoreTenant()

  const label = tenant?.display_name || tenant?.name || (isLoading ? 'Loading…' : 'No tenant')

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          className="flex items-center gap-2 bg-white/5 px-3 text-white hover:bg-white/10 hover:text-white data-[state=open]:!bg-white/15"
        >
          <Building2 className="size-4 shrink-0" />
          <span className="max-w-40 truncate text-sm font-medium">{label}</span>
          <ChevronsUpDown className="size-4 text-slate-400" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        <DropdownMenuLabel className="text-muted-foreground">Active tenant</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {tenants.length === 0 && <DropdownMenuItem disabled>No tenants yet</DropdownMenuItem>}
        {tenants.map((t) => (
          <DropdownMenuItem
            key={t.tenant_uuid}
            className="cursor-pointer"
            onClick={() => setTenantUuid(t.tenant_uuid)}
          >
            <span className="min-w-0 flex-1 truncate">{t.display_name || t.name}</span>
            {t.tenant_uuid === tenant?.tenant_uuid && <Check className="ml-2 size-4" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
