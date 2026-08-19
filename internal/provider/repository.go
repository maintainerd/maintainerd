package provider

import (
	"context"

	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/storage"
)

// Repository is the provider (driver registry) bounded context's data contract.
type Repository interface {
	GetTenantByUUID(ctx context.Context, tenantUUID uuid.UUID) (storage.Tenant, error)
	GetTenantByID(ctx context.Context, tenantID int64) (storage.Tenant, error)
	CreateProvider(ctx context.Context, arg storage.CreateProviderParams) (storage.Provider, error)
	GetProviderByUUID(ctx context.Context, providerUUID uuid.UUID) (storage.Provider, error)
	ListProvidersByTenant(ctx context.Context, arg storage.ListProvidersByTenantParams) ([]storage.Provider, error)
	CountProvidersByTenant(ctx context.Context, tenantID int64) (int64, error)
	UpdateProvider(ctx context.Context, arg storage.UpdateProviderParams) (storage.Provider, error)
	SoftDeleteProvider(ctx context.Context, providerUUID uuid.UUID) error
}
