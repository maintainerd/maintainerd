package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/platform/jsonutil"
	"github.com/maintainerd/core/internal/storage"
)

// Registration is the API/domain representation of a control-plane service Core
// tracks for a tenant (Auth, Secret, Docker, Database, ...). The row is the
// desired/observed record the reconciler acts on.
type Registration struct {
	UUID         uuid.UUID      `json:"service_uuid"`
	TenantUUID   uuid.UUID      `json:"tenant_uuid"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	IsSystem     bool           `json:"is_system"`
	Status       string         `json:"status"`
	Endpoint     string         `json:"endpoint"`
	Version      string         `json:"version"`
	Metadata     map[string]any `json:"metadata"`
	RegisteredAt *time.Time     `json:"registered_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Service is the service-registry business layer over the sqlc queries.
type Service struct {
	q Repository
}

func NewService(q Repository) *Service { return &Service{q: q} }

type CreateInput struct {
	TenantUUID uuid.UUID
	Name       string
	Kind       string
	IsSystem   bool
	Endpoint   string
	Version    string
	Metadata   map[string]any
}

type UpdateStatusInput struct {
	Status   string
	Endpoint string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Registration, error) {
	if in.Name == "" || in.Kind == "" {
		return nil, apperror.NewValidation("name and kind are required")
	}
	tenantRow, err := s.q.GetTenantByUUID(ctx, in.TenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, err
	}
	meta, err := marshalMeta(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateService(ctx, storage.CreateServiceParams{
		TenantID:     tenantRow.TenantID,
		Name:         in.Name,
		Kind:         in.Kind,
		Status:       "pending",
		Endpoint:     in.Endpoint,
		Version:      in.Version,
		Metadata:     meta,
		RegisteredAt: pgtype.Timestamptz{},
		IsSystem:     in.IsSystem,
	})
	if err != nil {
		return nil, err
	}
	reg := regFrom(row, tenantRow.TenantUuid)
	return &reg, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Registration, error) {
	row, err := s.q.GetServiceByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("service")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	reg := regFrom(row, tenantUUID)
	return &reg, nil
}

func (s *Service) ListByTenant(ctx context.Context, tenantUUID uuid.UUID, page, limit int) ([]Registration, int64, error) {
	tenantRow, err := s.q.GetTenantByUUID(ctx, tenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, 0, err
	}
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListServicesByTenant(ctx, storage.ListServicesByTenantParams{
		TenantID: tenantRow.TenantID,
		Limit:    int32(limit),
		Offset:   int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountServicesByTenant(ctx, tenantRow.TenantID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Registration, 0, len(rows))
	for _, r := range rows {
		out = append(out, regFrom(r, tenantRow.TenantUuid))
	}
	return out, total, nil
}

func (s *Service) UpdateStatus(ctx context.Context, id uuid.UUID, in UpdateStatusInput) (*Registration, error) {
	current, err := s.q.GetServiceByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("service")
	}
	if err != nil {
		return nil, err
	}
	status := current.Status
	if in.Status != "" {
		status = in.Status
	}
	endpoint := current.Endpoint
	if in.Endpoint != "" {
		endpoint = in.Endpoint
	}
	registeredAt := current.RegisteredAt
	// Stamp registered_at the first time a service reports running.
	if status == "running" && !registeredAt.Valid {
		registeredAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}

	row, err := s.q.UpdateServiceStatus(ctx, storage.UpdateServiceStatusParams{
		ServiceUuid:  id,
		Status:       status,
		Endpoint:     endpoint,
		RegisteredAt: registeredAt,
	})
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	reg := regFrom(row, tenantUUID)
	return &reg, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	row, err := s.q.GetServiceByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.NewNotFound("service")
	}
	if err != nil {
		return err
	}
	// System services are required for the platform to run — Core refuses to
	// remove them (and keeps them alive; see the reconciler).
	if row.IsSystem {
		return apperror.NewForbidden("system services are required by the platform and cannot be removed")
	}
	return s.q.SoftDeleteService(ctx, id)
}

// ListSystem returns the platform's system services — the ones Core must keep
// running at all times. The keep-alive reconciler consumes this feed.
func (s *Service) ListSystem(ctx context.Context) ([]Registration, error) {
	rows, err := s.q.ListSystemServices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Registration, 0, len(rows))
	for _, r := range rows {
		tenantUUID, err := s.resolveTenantUUID(ctx, r.TenantID)
		if err != nil {
			return nil, err
		}
		out = append(out, regFrom(r, tenantUUID))
	}
	return out, nil
}

func (s *Service) resolveTenantUUID(ctx context.Context, tenantID int64) (uuid.UUID, error) {
	t, err := s.q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantUuid, nil
}

// --- mapping helpers ---

func regFrom(row storage.Service, tenantUUID uuid.UUID) Registration {
	reg := Registration{
		UUID:       row.ServiceUuid,
		TenantUUID: tenantUUID,
		Name:       row.Name,
		Kind:       row.Kind,
		IsSystem:   row.IsSystem,
		Status:     row.Status,
		Endpoint:   row.Endpoint,
		Version:    row.Version,
		Metadata:   jsonutil.JSONToMap(row.Metadata),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
	if row.RegisteredAt.Valid {
		t := row.RegisteredAt.Time
		reg.RegisteredAt = &t
	}
	return reg
}

func marshalMeta(m map[string]any) ([]byte, error) {
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
