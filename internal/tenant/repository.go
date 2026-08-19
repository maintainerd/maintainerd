package tenant

import (
	"context"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the tenant bounded context's data contract — only the queries
// this context needs. *storage.Queries satisfies it, so the concrete sqlc code
// is injected at wiring time while tests can pass a mock.
type Repository interface {
	CreateTenant(ctx context.Context, arg storage.CreateTenantParams) (storage.Tenant, error)
	GetTenantByUUID(ctx context.Context, tenantUUID uuid.UUID) (storage.Tenant, error)
	ListTenants(ctx context.Context, arg storage.ListTenantsParams) ([]storage.Tenant, error)
	CountTenants(ctx context.Context) (int64, error)
	UpdateTenant(ctx context.Context, arg storage.UpdateTenantParams) (storage.Tenant, error)
	SoftDeleteTenant(ctx context.Context, tenantUUID uuid.UUID) error
}
