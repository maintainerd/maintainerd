package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/storage"
)

// fakeRepo is an in-memory Repository. Only the queries EnsureRegistered uses
// are meaningfully implemented; the rest satisfy the interface.
type fakeRepo struct {
	systemTenant *storage.Tenant
	byName       map[string]storage.Service

	created []storage.CreateServiceParams
	updated []storage.UpdateServiceStatusParams
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		systemTenant: &storage.Tenant{TenantID: 1, TenantUuid: uuid.New(), Name: "maintainerd", IsSystem: true},
		byName:       map[string]storage.Service{},
	}
}

func (f *fakeRepo) GetSystemTenant(context.Context) (storage.Tenant, error) {
	if f.systemTenant == nil {
		return storage.Tenant{}, pgx.ErrNoRows
	}
	return *f.systemTenant, nil
}

func (f *fakeRepo) GetServiceByTenantAndName(_ context.Context, arg storage.GetServiceByTenantAndNameParams) (storage.Service, error) {
	row, ok := f.byName[arg.Name]
	if !ok {
		return storage.Service{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeRepo) CreateService(_ context.Context, arg storage.CreateServiceParams) (storage.Service, error) {
	f.created = append(f.created, arg)
	row := storage.Service{
		ServiceUuid:  uuid.New(),
		TenantID:     arg.TenantID,
		Name:         arg.Name,
		Kind:         arg.Kind,
		Status:       arg.Status,
		Endpoint:     arg.Endpoint,
		IsSystem:     arg.IsSystem,
		RegisteredAt: arg.RegisteredAt,
	}
	f.byName[arg.Name] = row
	return row, nil
}

func (f *fakeRepo) UpdateServiceStatus(_ context.Context, arg storage.UpdateServiceStatusParams) (storage.Service, error) {
	f.updated = append(f.updated, arg)
	for name, row := range f.byName {
		if row.ServiceUuid == arg.ServiceUuid {
			row.Status = arg.Status
			row.Endpoint = arg.Endpoint
			row.RegisteredAt = arg.RegisteredAt
			f.byName[name] = row
			return row, nil
		}
	}
	return storage.Service{}, pgx.ErrNoRows
}

// --- unused by these tests, present to satisfy Repository -------------------

func (f *fakeRepo) GetTenantByUUID(context.Context, uuid.UUID) (storage.Tenant, error) {
	return storage.Tenant{}, pgx.ErrNoRows
}
func (f *fakeRepo) GetTenantByID(context.Context, int64) (storage.Tenant, error) {
	return storage.Tenant{}, pgx.ErrNoRows
}
func (f *fakeRepo) GetServiceByUUID(context.Context, uuid.UUID) (storage.Service, error) {
	return storage.Service{}, pgx.ErrNoRows
}
func (f *fakeRepo) ListServicesByTenant(context.Context, storage.ListServicesByTenantParams) ([]storage.Service, error) {
	return nil, nil
}
func (f *fakeRepo) CountServicesByTenant(context.Context, int64) (int64, error) { return 0, nil }
func (f *fakeRepo) ListSystemServices(context.Context) ([]storage.Service, error) {
	return nil, nil
}
func (f *fakeRepo) SoftDeleteService(context.Context, uuid.UUID) error { return nil }

func TestEnsureRegisteredCreatesTheRegistryRow(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)

	require.NoError(t, svc.EnsureRegistered(context.Background(), "storage", "Storage", true))

	require.Len(t, repo.created, 1)
	got := repo.created[0]
	assert.Equal(t, "storage", got.Name)
	assert.Equal(t, "Storage", got.Kind)
	assert.True(t, got.IsSystem)
	// A capability whose wiring exists is registered, not pending.
	assert.Equal(t, StatusRegistered, got.Status)
	assert.True(t, got.RegisteredAt.Valid)
	assert.Equal(t, repo.systemTenant.TenantID, got.TenantID)
}

func TestEnsureRegisteredAdvancesAPendingRow(t *testing.T) {
	repo := newFakeRepo()
	// The row setup inserted and nothing ever advanced — the bug this closes.
	repo.byName["secret"] = storage.Service{
		ServiceUuid: uuid.New(), TenantID: 1, Name: "secret", Kind: "Secret",
		Status: "pending", Endpoint: "secret:50051", IsSystem: true,
	}
	svc := NewService(repo)

	require.NoError(t, svc.EnsureRegistered(context.Background(), "secret", "Secret", true))

	require.Empty(t, repo.created, "an existing row must be advanced, not duplicated")
	require.Len(t, repo.updated, 1)
	assert.Equal(t, StatusRegistered, repo.updated[0].Status)
	assert.True(t, repo.updated[0].RegisteredAt.Valid)
	// The endpoint an operator (or a prior run) recorded is preserved.
	assert.Equal(t, "secret:50051", repo.updated[0].Endpoint)
}

func TestEnsureRegisteredIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)

	require.NoError(t, svc.EnsureRegistered(context.Background(), "storage", "Storage", true))
	require.NoError(t, svc.EnsureRegistered(context.Background(), "storage", "Storage", true))

	assert.Len(t, repo.created, 1)
	assert.Empty(t, repo.updated, "a converged row must not be rewritten")
}

func TestEnsureRegisteredNeverDowngradesARuntimeStatus(t *testing.T) {
	repo := newFakeRepo()
	// The agent has already observed this service running. That is further along
	// than this call knows about.
	repo.byName["secret"] = storage.Service{
		ServiceUuid: uuid.New(), TenantID: 1, Name: "secret", Kind: "Secret",
		Status: "running", RegisteredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	svc := NewService(repo)

	require.NoError(t, svc.EnsureRegistered(context.Background(), "secret", "Secret", true))
	assert.Empty(t, repo.updated, "live observation must not be overwritten with stale intent")
	assert.Empty(t, repo.created)
}

func TestEnsureRegisteredStampsAMissingRegisteredAt(t *testing.T) {
	repo := newFakeRepo()
	repo.byName["secret"] = storage.Service{
		ServiceUuid: uuid.New(), TenantID: 1, Name: "secret", Kind: "Secret", Status: "running",
	}
	svc := NewService(repo)

	require.NoError(t, svc.EnsureRegistered(context.Background(), "secret", "Secret", true))
	require.Len(t, repo.updated, 1)
	// Status is left alone; only the missing stamp is filled in.
	assert.Equal(t, "running", repo.updated[0].Status)
	assert.True(t, repo.updated[0].RegisteredAt.Valid)
}

func TestEnsureRegisteredValidatesInputAndTenant(t *testing.T) {
	t.Run("name and kind are required", func(t *testing.T) {
		svc := NewService(newFakeRepo())
		assert.Error(t, svc.EnsureRegistered(context.Background(), "", "Storage", true))
		assert.Error(t, svc.EnsureRegistered(context.Background(), "storage", "", true))
	})

	t.Run("no system tenant yet", func(t *testing.T) {
		repo := newFakeRepo()
		repo.systemTenant = nil
		err := NewService(repo).EnsureRegistered(context.Background(), "storage", "Storage", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "system tenant")
	})
}
