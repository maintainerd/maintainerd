package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/authctrl"
	"github.com/maintainerd/core/internal/deploytemplate"
	"github.com/maintainerd/core/internal/event"
	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/internal/project"
	"github.com/maintainerd/core/internal/provider"
	"github.com/maintainerd/core/internal/resource"
	"github.com/maintainerd/core/internal/secretctrl"
	"github.com/maintainerd/core/internal/service"
	"github.com/maintainerd/core/internal/setup"
	"github.com/maintainerd/core/internal/steward"
	"github.com/maintainerd/core/internal/storage"
	"github.com/maintainerd/core/internal/tenant"
)

// App is the wired domain layer: sqlc queries plus each domain's service + HTTP
// handler. It also exposes the services non-HTTP transports need (the gRPC
// AgentGateway uses AgentSvc + ResourceSvc; boot uses SetupOrch).
type App struct {
	Tenant   *tenant.Handler
	Project  *project.Handler
	Service  *service.Handler
	Provider *provider.Handler
	Agent    *agent.Handler
	Resource *resource.Handler
	Event    *event.Handler
	Setup    *setup.Handler
	Steward  *authctrl.Handler

	AgentSvc    *agent.Service
	ResourceSvc *resource.Service
	ServiceSvc  *service.Service
	TemplateSvc *deploytemplate.Service
	SetupOrch   *setup.Orchestrator

	// EventSvc is the platform escalation log's write side. The supervision loop
	// holds it; the HTTP surface is read-only.
	EventSvc *event.Service

	// Catalog is the control-plane desired state. Supervision reads the
	// availability tier from it, because tier is registration data rather than a
	// code property (plan/12-supervision-and-availability.md, decision 5).
	Catalog steward.Catalog

	// SecretClient is core's handle on maintainerd-secret. The boot gate probes
	// it before anything listens and /readyz probes it afterwards; the template
	// service writes provisioned credentials through it. Nil only in development
	// with no vault configured.
	SecretClient *secretctrl.Client

	// StewardRunner converges the control catalog through Auth's regular,
	// permission-verified RPCs once setup has issued Core its control identity.
	// Boot runs it in the background; the /steward routes drive it on demand.
	StewardRunner *authctrl.Runner

	// SecretSetupOrch drives maintainerd-secret's gRPC SetupService, the other
	// half of the core→secret relationship: SecretClient WRITES to a provisioned
	// vault, this one PROVISIONS it. Boot runs it in the background and /readyz
	// reports its state. Without it, an instance running MAINTAINERD_SECRET_MODE=core
	// has its REST wizard shut and nothing driving the gRPC path, so it has no
	// bootstrap path at all.
	SecretSetupOrch *setup.SecretOrchestrator
}

// New wires the domain layer over a pgx connection pool. The single
// *storage.Queries satisfies every domain's Repository interface. setupGate is
// the setup surface's self-guard (CORE_SETUP_TOKEN + optional admin-token
// verify) — resolved by the bootstrap because it depends on APP_ENV and the
// verifier the bootstrap builds.
func New(pool *pgxpool.Pool, setupGate setup.Gate) *App {
	q := storage.New(pool)

	agentSvc := agent.NewService(q)
	resourceSvc := resource.NewService(q)
	serviceSvc := service.NewService(q)
	eventSvc := event.NewService(q)

	setupCfg := setup.LoadConfig()

	// THE VAULT SEAM. Core's outbound handle on maintainerd-secret, built from
	// core's EXISTING control identity: the same private_key_jwt client that
	// drives auth's management RPCs, asking auth for a token whose audience is
	// secret's resource API. No second credential.
	//
	// The audience comes from setupCfg rather than a second variable so the token
	// core presents is minted for exactly the API core registered in auth — one
	// fact, one source. Scopes are the write actions a provision performs; a
	// credential with no user in the loop should not be able to read the vault
	// back.
	//
	// Nil when SECRET_BASE_URL is unset, which is only reachable in development
	// (config.Init refuses it otherwise). A nil client makes every provision that
	// needs secret material fail closed — it never makes one succeed by another
	// route.
	secretClient := secretctrl.New(secretctrl.Config{
		BaseURL:  config.SecretBaseURL,
		Audience: setupCfg.SecretAudience,
		Timeout:  config.DependencyProbeTimeout,
	}, authctrl.NewOutboundTokens(authctrl.LoadConfig(), q, setupCfg.SecretAudience,
		[]string{"secret:PutSecret"}))
	templateSvc := deploytemplate.NewService(q,
		secretctrl.NewCredentials(secretClient, config.SecretProject, config.SecretEnvironment))
	setupOrch := setup.NewOrchestrator(q, setupCfg)
	// Secret's setup driver takes the auth orchestrator as a dependency, which is
	// how "secret is provisioned strictly after auth" stops being incidental
	// ordering between two goroutines and becomes a gate the driver cannot skip.
	secretSetupOrch := setup.NewSecretOrchestrator(setupOrch, setupCfg)
	catalog := steward.BuiltinCatalog(setupCfg)

	// The post-setup control path. It shares the setup path's catalog and its
	// per-service key store: the two transports provision the SAME objects with
	// the SAME keys, they differ only in how they authenticate. Nothing here
	// dials or loads a credential yet — the runner connects lazily, because at
	// wiring time setup may not have issued Core its control identity.
	stewardKeys := steward.NewFileKeyStore(setupCfg.StewardKeyDir)
	stewardRunner := authctrl.NewRunner(
		authctrl.New(authctrl.LoadConfig(), q),
		catalog,
		stewardKeys,
		serviceSvc,
	)

	return &App{
		Tenant:   tenant.NewHandler(tenant.NewService(q)),
		Project:  project.NewHandler(project.NewService(q)),
		Service:  service.NewHandler(serviceSvc),
		Provider: provider.NewHandler(provider.NewService(q)),
		Agent:    agent.NewHandler(agentSvc),
		Resource: resource.NewHandler(resourceSvc),
		Event:    event.NewHandler(eventSvc),
		Setup:    setup.NewHandler(setupOrch, setupGate),
		Steward:  authctrl.NewHandler(stewardRunner),

		AgentSvc:      agentSvc,
		ResourceSvc:   resourceSvc,
		ServiceSvc:    serviceSvc,
		TemplateSvc:   templateSvc,
		SetupOrch:     setupOrch,
		EventSvc:      eventSvc,
		SecretClient:  secretClient,
		Catalog:       catalog,
		StewardRunner: stewardRunner,

		SecretSetupOrch: secretSetupOrch,
	}
}
