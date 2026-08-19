# maintainerd Console

The **main dashboard for maintainerd** — the control-plane UI for the core service.
It manages the declarative model core owns: **tenants → projects → resources**, plus
**services**, **providers** and **agents**. Modelled on the maintainerd auth console
(same React 19 + Vite + shadcn shell), stripped to the core domain.

## Stack

- React 19 + TypeScript, Vite 7
- TanStack Query (server state) · TanStack Table (listings)
- Tailwind v4 + shadcn/ui (Radix) components
- react-hook-form (form state)

## What it does

- **Dashboard** — per-tenant counts for projects / services / providers / agents.
- **Tenants** — the isolation boundary. The top-bar switcher picks the *active
  tenant*; every other listing is scoped to it.
- **Projects** — workloads within a tenant. A project's detail page hosts its
  **Resources**.
- **Resources** — declarative desired-state objects. The listing polls live and
  shows control-loop sync (`observed_generation` vs `generation`); a resource
  detail page shows `spec` (desired) vs `status` (observed).
- **Services / Providers / Agents** — the tenant's registered services, resource
  drivers, and executors.

It talks to the core REST API at `/api/v1` (same-origin). There is **no login yet**
— the core control plane currently requires no auth, so the app boots straight to
the dashboard. When core gains a system-Auth (IAM) gate, attach the token in
`src/services/api/client.ts`.

## Develop

The console is normally run through the **maintainerd-dev** stack, which serves it
hot-reloaded behind nginx at `https://console.maintainerd.local`:

```bash
# from the maintainerd-dev repo
./maintainerd up --profile=all -d          # auth + core stack + this console
# or with observability:
./maintainerd up --profile=all-observed -d
```

Standalone (Vite dev server, expects the core API reachable at `/api/v1`):

```bash
npm install
npm run dev
```

Scripts: `npm run dev` · `npm run build` (`tsc -b && vite build`) · `npm run lint`
· `npm run test`.

## Layout

```
src/
  services/api/          per-domain API modules (tenants, projects, services,
                         providers, agents, resources) + the shared client/config
  hooks/                 react-query hooks per domain (adapt {items,total} → {rows,total})
  context/               CoreTenantContext (active tenant) + ResourceScopeContext
  pages/                 one dir per domain: list + form + details
  components/            data-table, form, layout, sidebar, ui (shadcn) — the shell
```
