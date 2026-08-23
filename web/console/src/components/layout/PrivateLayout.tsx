import { Suspense, type CSSProperties } from "react"
import { Outlet, useLocation } from "react-router-dom"
import { AppSidebar } from "@/components/sidebar/AppSideBar"
import { AppTopNav } from "@/components/navigation/AppTopNav"
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar"
import { RouteFallback } from "@/components/layout/RouteFallback"
import { cn } from "@/lib/utils"

interface PrivateLayoutProps {
  fullWidth?: boolean
}

// Access gating (auth, first-run setup, tenant scope) is handled above this
// layout by ProtectedShell; this layout only renders the chrome.
//
// Chrome: the brand bar spans the full app, while the sidebar and content sit
// beneath it on the same calm canvas color as the login side-panel template.
export function PrivateLayout({ fullWidth = false }: PrivateLayoutProps) {
  const location = useLocation()

  return (
    <div className="min-h-svh bg-background">
      <SidebarProvider style={{ "--sidebar-width": "17rem" } as CSSProperties}>
        <AppTopNav />
        <AppSidebar variant="sidebar" />
        <SidebarInset className="min-w-0 bg-background pt-14">
          <main
            className={cn(
              "flex-1 px-4 py-6 sm:px-6 sm:py-8",
              !fullWidth && "w-full max-w-6xl mx-auto",
            )}
          >
            {/*
             * THE ROUTE-LEVEL SUSPENSE BOUNDARY BELONGS HERE, INSIDE THE SHELL,
             * AND IT MUST BE KEYED. Every page is React.lazy, and react-router
             * dispatches navigations inside startTransition. React will not
             * replace already-visible content with a fallback during a
             * transition, so a single boundary above <Routes> — which is where
             * this used to live — leaves React holding the ENTIRE tree at the
             * previous route for as long as the chunk takes to arrive: the page
             * does not change, the sidebar's active item does not move, and the
             * nav reads as frozen after the click landed.
             *
             * Keying by pathname makes each navigation a NEWLY MOUNTED boundary,
             * which React is free to show a fallback for immediately, and scopes
             * that fallback to the content region so the sidebar and brand bar
             * stay mounted, painted and interactive throughout.
             *
             * The key also remounts the page component when only a route param
             * changes (/tenants/a → /tenants/b), where it previously re-rendered
             * in place. That is the safer default here: it drops the outgoing
             * record's local form state instead of carrying it onto a different
             * record.
             */}
            <Suspense key={location.pathname} fallback={<RouteFallback />}>
              <Outlet />
            </Suspense>
          </main>
        </SidebarInset>
      </SidebarProvider>
    </div>
  )
}
