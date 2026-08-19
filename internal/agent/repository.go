package agent

import (
	"context"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the agent bounded context's data contract.
type Repository interface {
	GetTenantByUUID(ctx context.Context, tenantUUID uuid.UUID) (storage.Tenant, error)
	GetTenantByID(ctx context.Context, tenantID int64) (storage.Tenant, error)
	CreateAgent(ctx context.Context, arg storage.CreateAgentParams) (storage.Agent, error)
	GetAgentByUUID(ctx context.Context, agentUUID uuid.UUID) (storage.Agent, error)
	ListAgentsByTenant(ctx context.Context, arg storage.ListAgentsByTenantParams) ([]storage.Agent, error)
	CountAgentsByTenant(ctx context.Context, tenantID int64) (int64, error)
	UpdateAgentStatus(ctx context.Context, arg storage.UpdateAgentStatusParams) (storage.Agent, error)
	AgentHeartbeat(ctx context.Context, agentUUID uuid.UUID) (storage.Agent, error)
	SoftDeleteAgent(ctx context.Context, agentUUID uuid.UUID) error
}
