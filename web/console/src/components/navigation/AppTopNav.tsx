import { useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { CoreTenantSwitcher } from "@/components/navigation/CoreTenantSwitcher"
import {
  Boxes,
  Building2,
  Cpu,
  FolderKanban,
  HelpCircle,
  Plug,
  Plus,
  Server,
  BookOpen,
  Code2,
} from "lucide-react"

const resourceLinks = [
  { title: "Documentation", icon: BookOpen, href: "https://github.com/maintainerd" },
  { title: "API Reference", icon: Code2, href: "https://github.com/maintainerd" },
]

export function AppTopNav() {
  const navigate = useNavigate()

  const createItems = [
    { label: "Tenant", icon: Building2, route: "/tenants/create" },
    { label: "Project", icon: FolderKanban, route: "/projects/create" },
    { label: "Service", icon: Server, route: "/services/create" },
    { label: "Provider", icon: Plug, route: "/providers/create" },
    { label: "Agent", icon: Cpu, route: "/agents/create" },
  ]

  return (
    <header
      data-console-top-panel
      className="fixed inset-x-0 top-0 z-30 flex h-14 items-center border-b border-[#1e293b] bg-[#0f172a] px-4 text-white sm:px-6"
    >
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <SidebarTrigger
          data-console-top-control
          className="size-10 bg-white/5 text-slate-300 hover:bg-white/10 hover:text-white active:!bg-white/15 active:!text-white"
        />
        <button
          type="button"
          onClick={() => navigate("/dashboard")}
          className="flex items-center gap-2"
          aria-label="maintainerd home"
        >
          <Boxes className="size-6 shrink-0 text-white" />
          <span className="hidden md:inline">
            <span className="text-lg font-semibold leading-none text-white">maintainerd</span>
            <span className="ml-2 text-[11px] uppercase tracking-wide text-slate-400">Console</span>
          </span>
        </button>
        <div className="ml-4 hidden items-center gap-2 sm:flex">
          <CoreTenantSwitcher />
        </div>
      </div>

      <div className="ml-3 flex shrink-0 items-center gap-1.5">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              className="flex items-center gap-2 bg-white/5 px-3 text-white hover:bg-white/10 hover:text-white data-[state=open]:!bg-white/15"
            >
              <Plus className="size-4" />
              <span className="hidden text-sm font-medium sm:inline">Create</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuLabel className="text-muted-foreground">New…</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {createItems.map((item) => (
              <DropdownMenuItem key={item.label} className="cursor-pointer" onClick={() => navigate(item.route)}>
                <item.icon className="mr-2 h-4 w-4" />
                {item.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label="Help & resources"
              className="bg-white/5 text-slate-300 hover:bg-white/10 hover:text-white data-[state=open]:!bg-white/15"
            >
              <HelpCircle className="size-5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent className="w-48" align="end">
            <DropdownMenuLabel className="font-normal text-muted-foreground">Resources</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {resourceLinks.map((link) => (
              <DropdownMenuItem key={link.title} asChild className="cursor-pointer">
                <a href={link.href} target="_blank" rel="noopener noreferrer">
                  <link.icon className="mr-2 h-4 w-4" />
                  {link.title}
                </a>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
