// Package grpcserver serves the core.v1.AgentGateway contract this repo owns —
// the agent-facing control plane — backed by the sqlc-backed domain services.
//
// The surface is guarded the way the agent guards its own listener
// (../maintainerd-agent/internal/grpcserver): sdk-verified bearer tokens plus
// a method→permission allowlist, failing closed when auth is not configured.
// On top of transport auth, every RPC binds the caller's verified subject to
// the agent row it claims to be (see agent.Service.BindSubject) so a stolen
// agent UUID is worthless without the matching credential.
package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	sdkauth "github.com/maintainerd/sdk/auth"

	corev1 "github.com/maintainerd/core/gen/maintainerd/core/v1"
	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/resource"
)

// GuardMode is how the listener treats callers.
type GuardMode int

const (
	// GuardEnforced verifies tokens, permissions and agent identity binding
	// on every RPC — the only mode outside development.
	GuardEnforced GuardMode = iota
	// GuardDevOpen serves without authentication OR identity binding.
	// Permitted ONLY in development, and announced loudly at boot so an open
	// surface can never be a quiet surprise.
	GuardDevOpen
	// GuardHealthOnly refuses to serve the AgentGateway at all: only the
	// standard health protocol is registered. This is the fail-closed posture
	// when auth is required but not configured — the surface that hands out
	// workload specs and accepts status writes simply does not come up.
	GuardHealthOnly
)

// Guard is the resolved inbound-auth posture, decided at startup by the
// bootstrap (see cmd/server).
type Guard struct {
	Mode   GuardMode
	Verify VerifyFunc // required when Mode == GuardEnforced
	Reason string     // human-readable cause for DevOpen/HealthOnly logging
	Dev    bool       // development environment: enables gRPC reflection
}

// SDKVerify adapts the sdk verifier to the interceptor's VerifyFunc, mapping
// both permission claim shapes: the space-separated "scope" claim (parsed by
// the sdk) and a "permissions" array claim, either of which maintainerd-auth
// may mint.
func SDKVerify(v *sdkauth.Verifier) VerifyFunc {
	return func(_ context.Context, token string) (*Claims, error) {
		c, err := v.Verify(token)
		if err != nil {
			return nil, err
		}
		out := &Claims{Subject: c.Subject, Scopes: c.Scopes}
		if raw, ok := c.Raw["permissions"].([]any); ok {
			for _, p := range raw {
				if s, ok := p.(string); ok {
					out.Permissions = append(out.Permissions, s)
				}
			}
		}
		return out, nil
	}
}

// Options tunes the gateway's work-dispatch protocol; the bootstrap fills it
// from the environment (LEASE_TTL, ATTEMPT_BUDGET).
type Options struct {
	// EnforceBinding requires every RPC's verified subject to match the agent
	// row it operates on. False only in GuardDevOpen (no verified subjects
	// exist to compare).
	EnforceBinding bool
	// LeaseTTL is how long a dispatched work item stays out of the feed
	// before it may be re-dispatched (a crashed agent's lease expires rather
	// than parking the item forever).
	LeaseTTL time.Duration
	// AttemptBudget is how many failed convergence attempts a resource gets
	// before it parks as state 'failed' until a spec change.
	AttemptBudget int
	// Agent enrollment signing material.
	AgentCACertPEM []byte
	AgentCAKeyPEM  []byte
	AgentCertTTL   time.Duration
}

// AgentGateway implements corev1.AgentGatewayServer over the domain services.
type AgentGateway struct {
	corev1.UnimplementedAgentGatewayServiceServer
	agents    *agent.Service
	resources *resource.Service
	opts      Options
}

func NewAgentGateway(agents *agent.Service, resources *resource.Service, opts Options) *AgentGateway {
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = time.Minute
	}
	if opts.AttemptBudget < 1 {
		opts.AttemptBudget = 10
	}
	return &AgentGateway{agents: agents, resources: resources, opts: opts}
}

// Enroll is the one pre-identity gateway RPC. It consumes a one-time join token
// and signs the agent's CSR so subsequent RPCs can present a verified client
// certificate. Bearer auth and mTLS are intentionally enforced by the handler
// here, not the interceptor.
func (g *AgentGateway) Enroll(ctx context.Context, req *corev1.EnrollRequest) (*corev1.EnrollResponse, error) {
	if len(g.opts.AgentCACertPEM) == 0 || len(g.opts.AgentCAKeyPEM) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "agent enrollment CA is not configured")
	}
	id, err := uuid.Parse(req.GetAgentUuid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_uuid")
	}
	enrolled, err := g.agents.Enroll(ctx, agent.EnrollInput{
		AgentUUID: id,
		JoinToken: req.GetJoinToken(),
		CSRPem:    []byte(req.GetCsrPem()),
		CACertPEM: g.opts.AgentCACertPEM,
		CAKeyPEM:  g.opts.AgentCAKeyPEM,
		CertTTL:   g.opts.AgentCertTTL,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &corev1.EnrollResponse{
		CertificatePem:   string(enrolled.CertificatePEM),
		CaCertificatePem: string(enrolled.CACertPEM),
		ExpiresAt:        enrolled.ExpiresAt.Format(time.RFC3339),
	}, nil
}

// callerSubject returns the verified token subject for this RPC. With binding
// enforced, an empty subject is an error: a token without a subject cannot be
// pinned to an agent identity, so it must not act as one.
func (g *AgentGateway) callerSubject(ctx context.Context) (string, error) {
	if !g.opts.EnforceBinding {
		return "", nil
	}
	claims, ok := ClaimsFromContext(ctx)
	if !ok || claims.Subject == "" {
		return "", status.Error(codes.PermissionDenied, "token has no subject to bind an agent identity to")
	}
	return claims.Subject, nil
}

// requireAgent resolves the agent row for req.agent_uuid and, when binding is
// enforced, requires the caller's subject to match the row's bound subject.
func (g *AgentGateway) requireAgent(ctx context.Context, agentUUID string) (*agent.Agent, error) {
	id, err := uuid.Parse(agentUUID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_uuid")
	}
	if !g.opts.EnforceBinding {
		a, err := g.agents.Get(ctx, id)
		if err != nil {
			return nil, toStatus(err)
		}
		return a, nil
	}
	subject, err := g.callerSubject(ctx)
	if err != nil {
		return nil, err
	}
	a, err := g.agents.RequireSubject(ctx, id, subject)
	if err != nil {
		return nil, toStatus(err)
	}
	return a, nil
}

// Register marks the agent online and, when binding is enforced, pins the
// agent row to the caller's verified subject (first Register binds; a
// different subject is rejected — see agent.Service.BindSubject).
func (g *AgentGateway) Register(ctx context.Context, req *corev1.RegisterRequest) (*corev1.RegisterResponse, error) {
	id, err := uuid.Parse(req.GetAgentUuid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_uuid")
	}
	if g.opts.EnforceBinding {
		subject, err := g.callerSubject(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := g.agents.BindSubject(ctx, id, subject); err != nil {
			return nil, toStatus(err)
		}
	}
	if _, err := g.agents.Update(ctx, id, agent.UpdateInput{
		Status:       "online",
		Version:      req.GetVersion(),
		Capabilities: req.GetCapabilities(),
	}); err != nil {
		return nil, toStatus(err)
	}
	return &corev1.RegisterResponse{Ok: true}, nil
}

func (g *AgentGateway) Heartbeat(ctx context.Context, req *corev1.HeartbeatRequest) (*corev1.HeartbeatResponse, error) {
	a, err := g.requireAgent(ctx, req.GetAgentUuid())
	if err != nil {
		return nil, err
	}
	if _, err := g.agents.Heartbeat(ctx, a.UUID); err != nil {
		return nil, toStatus(err)
	}
	return &corev1.HeartbeatResponse{Ok: true}, nil
}

// PullWork hands the calling agent its slice of the feed — never a global
// view: items already assigned to the agent plus unassigned items it claims
// (sticky), each leased for LeaseTTL so no other agent can be handed the same
// item while this one works on it.
func (g *AgentGateway) PullWork(ctx context.Context, req *corev1.PullWorkRequest) (*corev1.PullWorkResponse, error) {
	a, err := g.requireAgent(ctx, req.GetAgentUuid())
	if err != nil {
		return nil, err
	}
	items, err := g.resources.ClaimForAgent(ctx, a.ID, int(req.GetMaxItems()), g.opts.LeaseTTL)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*corev1.WorkItem, 0, len(items))
	for _, it := range items {
		specJSON, err := buildEnvelope(it)
		if err != nil {
			slog.Error("pullwork: envelope build failed — item skipped", "resource", it.UUID, "err", err)
			continue
		}
		out = append(out, &corev1.WorkItem{
			ResourceUuid: it.UUID.String(),
			Kind:         it.Kind,
			Name:         it.Name,
			SpecJson:     specJSON,
			Generation:   it.Generation,
		})
	}
	return &corev1.PullWorkResponse{Items: out}, nil
}

// envelope is the wire shape of a WorkItem's spec_json. Its consumer twin is
// ../maintainerd-agent/internal/worker/envelope.go — keep the two in lockstep:
//
//	{"workload": <kit runtime.WorkloadSpec JSON>, "tier": "system"|"", "teardown": bool}
type envelope struct {
	Workload any    `json:"workload"`
	Tier     string `json:"tier,omitempty"`
	Teardown bool   `json:"teardown,omitempty"`
}

// buildEnvelope wraps a work item's stored spec in the envelope the agent's
// worker parses:
//   - workload: the resource's spec as stored. If the stored spec already
//     carries a top-level "workload" object (an operator persisted a
//     pre-enveloped spec), that inner object is passed through unchanged so it
//     is never double-wrapped.
//   - tier: "system" when the resource's metadata says {"tier":"system"} —
//     the agent keeps system-tier workloads alive even while Core is down.
//   - teardown: true when the resource is in state 'deleting'; the agent
//     removes the workload and reports "removed", which finalizes the delete.
func buildEnvelope(it resource.WorkItem) (string, error) {
	env := envelope{Workload: any(it.Spec)}
	if w, ok := it.Spec["workload"].(map[string]any); ok && len(w) > 0 {
		env.Workload = w
	}
	if t, _ := it.Metadata["tier"].(string); t == "system" {
		env.Tier = "system"
	}
	if it.State == "deleting" {
		env.Teardown = true
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReportStatus records the agent's observations. Ownership is enforced per
// report — a report for a resource assigned to a different agent (or to no
// agent) is REJECTED and counted, without failing the rest of the batch, so
// one bad entry cannot make an agent drop a whole batch of honest reports.
func (g *AgentGateway) ReportStatus(ctx context.Context, req *corev1.ReportStatusRequest) (*corev1.ReportStatusResponse, error) {
	a, err := g.requireAgent(ctx, req.GetAgentUuid())
	if err != nil {
		return nil, err
	}
	var accepted int32
	var rejected int
	for _, rep := range req.GetReports() {
		id, err := uuid.Parse(rep.GetResourceUuid())
		if err != nil {
			rejected++
			continue
		}
		var statusMap map[string]any
		if raw := rep.GetStatusJson(); raw != "" {
			_ = json.Unmarshal([]byte(raw), &statusMap)
		}
		if _, err := g.resources.ApplyAgentReport(ctx, a.ID, id, resource.AgentReportInput{
			State:              rep.GetState(),
			Status:             statusMap,
			ObservedGeneration: rep.GetObservedGeneration(),
		}, g.opts.AttemptBudget); err != nil {
			rejected++
			continue
		}
		accepted++
	}
	if rejected > 0 {
		slog.Warn("report status: reports rejected", "agent", a.UUID, "rejected", rejected, "accepted", accepted)
	}
	return &corev1.ReportStatusResponse{Accepted: accepted}, nil
}

// toStatus maps domain errors to gRPC status codes.
func toStatus(err error) error {
	var notFound *apperror.NotFoundError
	if errors.As(err, &notFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	var validation *apperror.ValidationError
	if errors.As(err, &validation) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	var forbidden *apperror.ForbiddenError
	if errors.As(err, &forbidden) {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// Serve starts the gRPC server per the guard's posture and stops it
// gracefully when ctx is cancelled. Fail-closed ladder:
//   - GuardEnforced: auth interceptor on every RPC.
//   - GuardDevOpen: unauthenticated, development only, loud boot warning
//     naming every disabled guard.
//   - GuardHealthOnly: the AgentGateway is NOT registered — only grpc-health
//     serves, reporting NOT_SERVING for the gateway service.
//
// gRPC reflection is a discovery aid and an attacker's site map; it registers
// in development only.
func Serve(ctx context.Context, addr string, gw *AgentGateway, guard Guard, tlsOpts TLSOptions) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	var opts []grpc.ServerOption
	if tlsCfg, err := serverTLSConfig(tlsOpts); err != nil {
		return err
	} else if tlsCfg != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}
	switch guard.Mode {
	case GuardEnforced:
		interceptors := []grpc.UnaryServerInterceptor{}
		if tlsOpts.RequireClientCert {
			interceptors = append(interceptors, RequireClientCertUnaryInterceptor())
		}
		interceptors = append(interceptors, AuthUnaryInterceptor(guard.Verify))
		opts = append(opts, grpc.ChainUnaryInterceptor(interceptors...))
	case GuardDevOpen:
		slog.Warn("SECURITY: AgentGateway gRPC surface is UNAUTHENTICATED (development only)",
			"disabled_guards", "bearer-token verification, permission core:agent:gateway, agent identity binding (bound_subject)",
			"reason", guard.Reason,
		)
	case GuardHealthOnly:
		slog.Error("gRPC AgentGateway REFUSING to start — serving health only", "reason", guard.Reason)
	}

	gs := grpc.NewServer(opts...)
	hs := health.NewServer()
	healthpb.RegisterHealthServer(gs, hs)

	if guard.Mode == GuardHealthOnly {
		hs.SetServingStatus("maintainerd.core.v1.AgentGatewayService", healthpb.HealthCheckResponse_NOT_SERVING)
	} else {
		corev1.RegisterAgentGatewayServiceServer(gs, gw)
		hs.SetServingStatus("maintainerd.core.v1.AgentGatewayService", healthpb.HealthCheckResponse_SERVING)
	}

	if guard.Dev {
		reflection.Register(gs)
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down grpc server")
		gs.GracefulStop()
	}()

	slog.Info("grpc server listening", "addr", addr, "guard", guardModeName(guard.Mode))
	if err := gs.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

func guardModeName(m GuardMode) string {
	switch m {
	case GuardEnforced:
		return "enforced"
	case GuardDevOpen:
		return "dev-open"
	case GuardHealthOnly:
		return "health-only"
	default:
		return "unknown"
	}
}
