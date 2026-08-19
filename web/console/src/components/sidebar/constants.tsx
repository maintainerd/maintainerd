import {
  LayoutDashboard,
  Building2,
  FolderKanban,
  Server,
  Plug,
  Cpu,
  type LucideIcon,
} from "lucide-react"
import type { ComponentType } from "react"

// Sidenav icons: lucide, wrapped so the active item renders a bolder stroke and
// inactive items a thinner one (mirroring the active/inactive weight used for the
// nav text). Icons inherit the nav item's text color.
const li =
  (IconCmp: LucideIcon): ComponentType<{ className?: string; active?: boolean }> =>
  ({ className, active }) =>
    <IconCmp className={className} strokeWidth={active ? 2.25 : 1.5} />

export const data = {
  navSections: [
    {
      label: "Overview",
      items: [
        {
          title: "Dashboard",
          route: "/dashboard",
          icon: li(LayoutDashboard),
        },
      ],
    },
    {
      label: "Control Plane",
      items: [
        {
          title: "Tenants",
          route: "/tenants",
          icon: li(Building2),
        },
        {
          title: "Projects",
          route: "/projects",
          icon: li(FolderKanban),
          activeRoutes: ["/resources"],
        },
        {
          title: "Services",
          route: "/services",
          icon: li(Server),
        },
        {
          title: "Providers",
          route: "/providers",
          icon: li(Plug),
        },
        {
          title: "Agents",
          route: "/agents",
          icon: li(Cpu),
        },
      ],
    },
  ],
}
