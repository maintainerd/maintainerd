package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/platform/jsonutil"
	"github.com/maintainerd/core/internal/storage"
)

// Provider binds a resource kind to a concrete driver for a tenant
// (e.g. resource_kind=Database, driver=container | awsRDS).
type Provider struct {
	UUID         uuid.UUID      `json:"provider_uuid"`
	TenantUUID   uuid.UUID      `json:"tenant_uuid"`
	Name         string         `json:"name"`
	ResourceKind string         `json:"resource_kind"`
	Driver       string         `json:"driver"`
	Config       map[string]any `json:"config"`
	Status       string         `json:"status"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type Service struct{ q Repository }

func NewService(q Repository) *Service { return &Service{q: q} }

type CreateInput struct {
	TenantUUID   uuid.UUID
	Name         string
	ResourceKind string
	Driver       string
	Config       map[string]any
	Metadata     map[string]any
}

type UpdateInput struct {
	Driver   string
	Config   map[string]any
	Status   string
	Metadata map[string]any
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Provider, error) {
	if in.Name == "" || in.ResourceKind == "" || in.Driver == "" {
		return nil, apperror.NewValidation("name, resource_kind and driver are required")
	}
	t, err := s.q.GetTenantByUUID(ctx, in.TenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, err
	}
	config, err := marshalMap(in.Config)
	if err != nil {
		return nil, apperror.NewValidation("invalid config")
	}
	meta, err := marshalMap(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateProvider(ctx, storage.CreateProviderParams{
		TenantID:     t.TenantID,
		Name:         in.Name,
		ResourceKind: in.ResourceKind,
		Driver:       in.Driver,
		Config:       config,
		Status:       "active",
		Metadata:     meta,
	})
	if err != nil {
		return nil, err
	}
	p := toProvider(row, t.TenantUuid)
	return &p, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Provider, error) {
	row, err := s.q.GetProviderByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("provider")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	p := toProvider(row, tenantUUID)
	return &p, nil
}

func (s *Service) List(ctx context.Context, tenantUUID uuid.UUID, page, limit int) ([]Provider, int64, error) {
	t, err := s.q.GetTenantByUUID(ctx, tenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, 0, err
	}
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListProvidersByTenant(ctx, storage.ListProvidersByTenantParams{
		TenantID: t.TenantID,
		Limit:    int32(limit),
		Offset:   int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountProvidersByTenant(ctx, t.TenantID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Provider, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProvider(r, t.TenantUuid))
	}
	return out, total, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Provider, error) {
	current, err := s.q.GetProviderByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("provider")
	}
	if err != nil {
		return nil, err
	}
	driver := current.Driver
	if in.Driver != "" {
		driver = in.Driver
	}
	status := current.Status
	if in.Status != "" {
		status = in.Status
	}
	config := current.Config
	if in.Config != nil {
		if config, err = marshalMap(in.Config); err != nil {
			return nil, apperror.NewValidation("invalid config")
		}
	}
	meta := current.Metadata
	if in.Metadata != nil {
		if meta, err = marshalMap(in.Metadata); err != nil {
			return nil, apperror.NewValidation("invalid metadata")
		}
	}
	row, err := s.q.UpdateProvider(ctx, storage.UpdateProviderParams{
		ProviderUuid: id,
		Driver:       driver,
		Config:       config,
		Status:       status,
		Metadata:     meta,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("provider")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	p := toProvider(row, tenantUUID)
	return &p, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.SoftDeleteProvider(ctx, id)
}

func (s *Service) resolveTenantUUID(ctx context.Context, tenantID int64) (uuid.UUID, error) {
	t, err := s.q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantUuid, nil
}

func toProvider(m storage.Provider, tenantUUID uuid.UUID) Provider {
	return Provider{
		UUID:         m.ProviderUuid,
		TenantUUID:   tenantUUID,
		Name:         m.Name,
		ResourceKind: m.ResourceKind,
		Driver:       m.Driver,
		Config:       jsonutil.JSONToMap(m.Config),
		Status:       m.Status,
		Metadata:     jsonutil.JSONToMap(m.Metadata),
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

func marshalMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func normalizePage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return page, limit
}
