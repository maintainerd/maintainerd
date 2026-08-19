package project

import (
	"context"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the project bounded context's data contract.
// *storage.Queries satisfies it; tests can pass a mock.
type Repository interface {
	GetTenantByUUID(ctx context.Context, tenantUUID uuid.UUID) (storage.Tenant, error)
	GetTenantByID(ctx context.Context, tenantID int64) (storage.Tenant, error)
	CreateProject(ctx context.Context, arg storage.CreateProjectParams) (storage.Project, error)
	GetProjectByUUID(ctx context.Context, projectUUID uuid.UUID) (storage.Project, error)
	ListProjectsByTenant(ctx context.Context, arg storage.ListProjectsByTenantParams) ([]storage.Project, error)
	CountProjectsByTenant(ctx context.Context, tenantID int64) (int64, error)
	UpdateProject(ctx context.Context, arg storage.UpdateProjectParams) (storage.Project, error)
	SoftDeleteProject(ctx context.Context, projectUUID uuid.UUID) error
}
