package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/project"
	"github.com/maintainerd/core/internal/provider"
	"github.com/maintainerd/core/internal/resource"
	"github.com/maintainerd/core/internal/service"
	"github.com/maintainerd/core/internal/storage"
	"github.com/maintainerd/core/internal/tenant"
)

// App is the wired domain layer: sqlc queries plus each domain's service + HTTP
// handler. It also exposes the services non-HTTP transports need (the gRPC
// AgentGateway uses AgentSvc + ResourceSvc).
type App struct {
	Tenant   *tenant.Handler
	Project  *project.Handler
	Service  *service.Handler
	Provider *provider.Handler
	Agent    *agent.Handler
	Resource *resource.Handler

	AgentSvc    *agent.Service
	ResourceSvc *resource.Service
}

// New wires the domain layer over a pgx connection pool. The single
// *storage.Queries satisfies every domain's Repository interface.
func New(pool *pgxpool.Pool) *App {
	q := storage.New(pool)

	agentSvc := agent.NewService(q)
	resourceSvc := resource.NewService(q)

	return &App{
		Tenant:   tenant.NewHandler(tenant.NewService(q)),
		Project:  project.NewHandler(project.NewService(q)),
		Service:  service.NewHandler(service.NewService(q)),
		Provider: provider.NewHandler(provider.NewService(q)),
		Agent:    agent.NewHandler(agentSvc),
		Resource: resource.NewHandler(resourceSvc),

		AgentSvc:    agentSvc,
		ResourceSvc: resourceSvc,
	}
}
