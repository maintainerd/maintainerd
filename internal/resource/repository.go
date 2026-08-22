package resource

import (
	"context"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the resource bounded context's data contract. Resources are the
// declarative core: a desired spec reconciled toward an observed status.
type Repository interface {
	GetTenantByID(ctx context.Context, tenantID int64) (storage.Tenant, error)
	GetProjectByUUID(ctx context.Context, projectUUID uuid.UUID) (storage.Project, error)
	GetProjectByID(ctx context.Context, projectID int64) (storage.Project, error)
	GetProviderByUUID(ctx context.Context, providerUUID uuid.UUID) (storage.Provider, error)
	CreateResource(ctx context.Context, arg storage.CreateResourceParams) (storage.Resource, error)
	GetResourceByUUID(ctx context.Context, resourceUUID uuid.UUID) (storage.Resource, error)
	ListResourcesByProject(ctx context.Context, arg storage.ListResourcesByProjectParams) ([]storage.Resource, error)
	CountResourcesByProject(ctx context.Context, projectID int64) (int64, error)
	UpdateResourceSpec(ctx context.Context, arg storage.UpdateResourceSpecParams) (storage.Resource, error)
	UpdateResourceStatus(ctx context.Context, arg storage.UpdateResourceStatusParams) (storage.Resource, error)
	ListOutOfSyncResources(ctx context.Context, limit int32) ([]storage.Resource, error)
	ClaimAgentWork(ctx context.Context, arg storage.ClaimAgentWorkParams) ([]storage.Resource, error)
	ApplyAgentReport(ctx context.Context, arg storage.ApplyAgentReportParams) (storage.Resource, error)
	MarkResourceDeleting(ctx context.Context, resourceUUID uuid.UUID) error
}
