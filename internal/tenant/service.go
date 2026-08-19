package tenant

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

// Tenant is the API/domain representation of a core tenant — a thin record
// keyed to an Auth tenant (Auth owns identity/memberships; Core keys its
// resource inventory to auth_tenant_uuid).
type Tenant struct {
	UUID           uuid.UUID      `json:"tenant_uuid"`
	AuthTenantUUID *uuid.UUID     `json:"auth_tenant_uuid,omitempty"`
	Name           string         `json:"name"`
	DisplayName    string         `json:"display_name"`
	Status         string         `json:"status"`
	IsSystem       bool           `json:"is_system"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// Service is the tenant business layer. It wraps the sqlc-generated queries.
type Service struct {
	q Repository
}

func NewService(q Repository) *Service { return &Service{q: q} }

type CreateInput struct {
	Name           string
	DisplayName    string
	Status         string
	IsSystem       bool
	AuthTenantUUID *uuid.UUID
	Metadata       map[string]any
}

type UpdateInput struct {
	DisplayName    string
	Status         string
	AuthTenantUUID *uuid.UUID
	Metadata       map[string]any
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Tenant, error) {
	if in.Name == "" {
		return nil, apperror.NewValidation("name is required")
	}
	status := in.Status
	if status == "" {
		status = "active"
	}
	meta, err := marshalMeta(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateTenant(ctx, storage.CreateTenantParams{
		Name:           in.Name,
		DisplayName:    in.DisplayName,
		Status:         status,
		IsSystem:       in.IsSystem,
		AuthTenantUuid: toPgUUID(in.AuthTenantUUID),
		Metadata:       meta,
	})
	if err != nil {
		return nil, err
	}
	return ptrTenant(row), nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Tenant, error) {
	row, err := s.q.GetTenantByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, err
	}
	return ptrTenant(row), nil
}

func (s *Service) List(ctx context.Context, page, limit int) ([]Tenant, int64, error) {
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListTenants(ctx, storage.ListTenantsParams{
		Limit:  int32(limit),
		Offset: int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountTenants(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Tenant, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTenant(r))
	}
	return out, total, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Tenant, error) {
	current, err := s.q.GetTenantByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, err
	}

	displayName := current.DisplayName
	if in.DisplayName != "" {
		displayName = in.DisplayName
	}
	status := current.Status
	if in.Status != "" {
		status = in.Status
	}
	authUUID := current.AuthTenantUuid
	if in.AuthTenantUUID != nil {
		authUUID = toPgUUID(in.AuthTenantUUID)
	}
	meta := current.Metadata
	if in.Metadata != nil {
		if meta, err = marshalMeta(in.Metadata); err != nil {
			return nil, apperror.NewValidation("invalid metadata")
		}
	}

	row, err := s.q.UpdateTenant(ctx, storage.UpdateTenantParams{
		TenantUuid:     id,
		DisplayName:    displayName,
		Status:         status,
		AuthTenantUuid: authUUID,
		Metadata:       meta,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, err
	}
	return ptrTenant(row), nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	current, err := s.q.GetTenantByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.NewNotFound("tenant")
	}
	if err != nil {
		return err
	}
	// The system (root) tenant is required for the platform to run — it cannot be removed.
	if current.IsSystem {
		return apperror.NewForbidden("the system tenant is required by the platform and cannot be removed")
	}
	return s.q.SoftDeleteTenant(ctx, id)
}

// GetSystem returns the platform's root (system) tenant.
func (s *Service) GetSystem(ctx context.Context) (*Tenant, error) {
	row, err := s.q.GetSystemTenant(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("system tenant")
	}
	if err != nil {
		return nil, err
	}
	return ptrTenant(row), nil
}

// --- mapping helpers ---

func toTenant(m storage.Tenant) Tenant {
	t := Tenant{
		UUID:        m.TenantUuid,
		Name:        m.Name,
		DisplayName: m.DisplayName,
		Status:      m.Status,
		IsSystem:    m.IsSystem,
		Metadata:    jsonutil.JSONToMap(m.Metadata),
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
	if m.AuthTenantUuid.Valid {
		u := uuid.UUID(m.AuthTenantUuid.Bytes)
		t.AuthTenantUUID = &u
	}
	return t
}

func ptrTenant(m storage.Tenant) *Tenant { t := toTenant(m); return &t }

func toPgUUID(u *uuid.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *u, Valid: true}
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
