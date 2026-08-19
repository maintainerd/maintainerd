// Package grpcserver serves the core.v1.AgentGateway contract this repo owns —
// the agent-facing control plane — backed by the sqlc-backed domain services.
package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	corev1 "github.com/maintainerd/core/gen/maintainerd/core/v1"
	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/resource"
)

// AgentGateway implements corev1.AgentGatewayServer over the domain services.
type AgentGateway struct {
	corev1.UnimplementedAgentGatewayServiceServer
	agents    *agent.Service
	resources *resource.Service
}

func NewAgentGateway(agents *agent.Service, resources *resource.Service) *AgentGateway {
	return &AgentGateway{agents: agents, resources: resources}
}

func (g *AgentGateway) Register(ctx context.Context, req *corev1.RegisterRequest) (*corev1.RegisterResponse, error) {
	id, err := uuid.Parse(req.GetAgentUuid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_uuid")
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
	id, err := uuid.Parse(req.GetAgentUuid())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_uuid")
	}
	if _, err := g.agents.Heartbeat(ctx, id); err != nil {
		return nil, toStatus(err)
	}
	return &corev1.HeartbeatResponse{Ok: true}, nil
}

func (g *AgentGateway) PullWork(ctx context.Context, req *corev1.PullWorkRequest) (*corev1.PullWorkResponse, error) {
	items, err := g.resources.OutOfSync(ctx, int(req.GetMaxItems()))
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*corev1.WorkItem, 0, len(items))
	for _, it := range items {
		specJSON, _ := json.Marshal(it.Spec)
		out = append(out, &corev1.WorkItem{
			ResourceUuid: it.UUID.String(),
			Kind:         it.Kind,
			Name:         it.Name,
			SpecJson:     string(specJSON),
			Generation:   it.Generation,
		})
	}
	return &corev1.PullWorkResponse{Items: out}, nil
}

func (g *AgentGateway) ReportStatus(ctx context.Context, req *corev1.ReportStatusRequest) (*corev1.ReportStatusResponse, error) {
	var accepted int32
	for _, rep := range req.GetReports() {
		id, err := uuid.Parse(rep.GetResourceUuid())
		if err != nil {
			continue
		}
		var statusMap map[string]any
		if raw := rep.GetStatusJson(); raw != "" {
			_ = json.Unmarshal([]byte(raw), &statusMap)
		}
		if _, err := g.resources.UpdateStatus(ctx, id, resource.UpdateStatusInput{
			Status:             statusMap,
			State:              rep.GetState(),
			ObservedGeneration: rep.GetObservedGeneration(),
		}); err != nil {
			continue
		}
		accepted++
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
	return status.Error(codes.Internal, err.Error())
}

// Serve starts the gRPC server (AgentGateway + gRPC health + reflection) and
// stops it gracefully when ctx is cancelled.
func Serve(ctx context.Context, addr string, agents *agent.Service, resources *resource.Service) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	gs := grpc.NewServer()
	corev1.RegisterAgentGatewayServiceServer(gs, NewAgentGateway(agents, resources))

	hs := health.NewServer()
	hs.SetServingStatus("maintainerd.core.v1.AgentGatewayService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(gs, hs)
	reflection.Register(gs)

	go func() {
		<-ctx.Done()
		slog.Info("shutting down grpc server")
		gs.GracefulStop()
	}()

	slog.Info("grpc server listening", "addr", addr)
	if err := gs.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}
