# Changelog

All notable changes to the maintainerd console will be documented in this file.

## [0.1.0] - 2026-08-19

### Added

- Initial core console — the main dashboard for maintainerd, cloned from the auth
  console shell and stripped to the core domain.
- Domains: Dashboard, Tenants, Projects, Resources (project-scoped), Services,
  Providers, Agents — each with list / create / edit / detail views.
- Active-tenant switcher (top bar) scoping every tenant-owned listing; resources
  poll live and surface control-loop sync (`observed_generation` vs `generation`).
- Data layer: per-domain API modules + react-query hooks against the core REST
  API (`/api/v1`), no auth (core control plane is currently unauthenticated).
