package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the service-registry bounded context's data contract. It reaches
// tenants only to resolve a tenant's internal id/uuid; all writes stay on the
// services table. *storage.Queries satisfies it; tests can pass a mock.
type Repository interface {
	GetTenantByUUID(ctx context.Context, tenantUUID uuid.UUID) (storage.Tenant, error)
	GetTenantByID(ctx context.Context, tenantID int64) (storage.Tenant, error)
	// GetSystemTenant resolves the install's system tenant, which is the tenant
	// control-plane capabilities are registered under (see EnsureRegistered).
	GetSystemTenant(ctx context.Context) (storage.Tenant, error)
	CreateService(ctx context.Context, arg storage.CreateServiceParams) (storage.Service, error)
	GetServiceByUUID(ctx context.Context, serviceUUID uuid.UUID) (storage.Service, error)
	GetServiceByTenantAndName(ctx context.Context, arg storage.GetServiceByTenantAndNameParams) (storage.Service, error)
	ListServicesByTenant(ctx context.Context, arg storage.ListServicesByTenantParams) ([]storage.Service, error)
	CountServicesByTenant(ctx context.Context, tenantID int64) (int64, error)
	UpdateServiceStatus(ctx context.Context, arg storage.UpdateServiceStatusParams) (storage.Service, error)
	ListSystemServices(ctx context.Context) ([]storage.Service, error)
	SoftDeleteService(ctx context.Context, serviceUUID uuid.UUID) error
}
